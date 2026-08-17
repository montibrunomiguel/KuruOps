// Package mcpclient speaks the Model Context Protocol to a tenant-registered
// MCP server (see mcp_servers in db/migrations/0007_llm_mcp.up.sql): the
// handshake, listing available tools, and invoking one. It implements only
// the "Streamable HTTP" transport (a single endpoint, JSON-RPC 2.0 over
// POST, optionally with an SSE response) — stdio and sse as configured in
// mcp_servers.transport are accepted at the config level but not dialable
// yet, see NewFromConfig. Nothing here has been exercised against a live
// MCP server (none was available while building this) — treat it as a
// solid starting point to validate against a real server before relying on
// it for anything side-effecting.
package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// tracer's provider is whatever telemetry.Setup registered globally (a
// no-op unless OTEL_EXPORTER_OTLP_ENDPOINT is set) -- see post below, this
// package's one choke point every call/notify eventually funnels through.
var tracer = otel.Tracer("argusops/mcpclient")

const (
	protocolVersion = "2025-03-26"
	clientName      = "argusops"
	clientVersion   = "0.1.0"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message)
}

// Client talks to one MCP server over Streamable HTTP. Not safe for
// concurrent use by multiple goroutines against the same session (the
// session id is stateful) — callers should create one Client per in-flight
// operation, which matches how internal/service/mcp_tool_service.go uses it.
type Client struct {
	endpoint   string
	authToken  string
	httpClient *http.Client
	sessionID  string
	nextID     int64
}

func New(endpoint, authToken string) *Client {
	return &Client{
		endpoint:   endpoint,
		authToken:  authToken,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

// Initialize performs the MCP handshake and must be called before ListTools
// or CallTool. It also sends the required "notifications/initialized"
// follow-up per the spec.
func (c *Client) Initialize(ctx context.Context) error {
	var result initializeResult
	if err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": clientName, "version": clientVersion},
	}, &result); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("send initialized notification: %w", err)
	}
	return nil
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type listToolsResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// ListTools returns every tool the server exposes, following pagination
// cursors until exhausted. This is the raw catalog — it is NOT the
// allow-list; mcp_servers.allowed_tools (set by an admin from this list) is
// what actually gates what the analysis agent may use, see
// service.EvaluateToolInvocation.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var all []Tool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result listToolsResult
		if err := c.call(ctx, "tools/list", params, &result); err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		all = append(all, result.Tools...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return all, nil
}

type ToolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// CallTool invokes tool with args and returns its result. The caller is
// responsible for having already checked service.EvaluateToolInvocation —
// this method has no notion of allow-lists or approval, it just makes the
// call.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error) {
	var result ToolCallResult
	if err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	}, &result); err != nil {
		return nil, fmt.Errorf("tools/call %s: %w", name, err)
	}
	return &result, nil
}

// call sends a JSON-RPC request expecting a response and decodes result
// into out.
func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	c.nextID++
	req := rpcRequest{JSONRPC: "2.0", ID: c.nextID, Method: method, Params: params}

	body, err := c.post(ctx, req)
	if err != nil {
		return err
	}

	resp, err := parseRPCResponse(body, req.ID)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decode result: %w", err)
		}
	}
	return nil
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (c *Client) notify(ctx context.Context, method string, params any) error {
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	_, err := c.post(ctx, req)
	return err
}

func (c *Client) post(ctx context.Context, req rpcRequest) ([]byte, error) {
	ctx, span := tracer.Start(ctx, "mcp.request")
	defer span.End()
	span.SetAttributes(attribute.String("mcp.method", req.Method))

	payload, err := json.Marshal(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if c.authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()
	span.SetAttributes(attribute.Int("http.status_code", httpResp.StatusCode))

	if sid := httpResp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	if httpResp.StatusCode == http.StatusAccepted {
		// Accepted with no body is the valid response to a notification
		// (e.g. notifications/initialized) -- nothing more to read.
		return nil, nil
	}

	bodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("read response: %w", err)
	}
	if httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp server returned %d: %s", httpResp.StatusCode, string(bodyBytes))
	}

	contentType := httpResp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		return extractJSONFromSSE(bodyBytes)
	}
	return bodyBytes, nil
}

// extractJSONFromSSE pulls the JSON payload out of a (non-streamed, already
// fully-read) SSE response body -- takes the last "data:" line, which for a
// single-response call is the JSON-RPC message. A server that streams
// multiple events per call (progress notifications, etc.) needs a real
// incremental SSE reader; this is intentionally the simple case.
func extractJSONFromSSE(body []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	var last string
	for scanner.Scan() {
		line := scanner.Text()
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			last = strings.TrimSpace(data)
		}
	}
	if last == "" {
		return nil, fmt.Errorf("no data event found in SSE response")
	}
	return []byte(last), nil
}

func parseRPCResponse(body []byte, expectedID int64) (*rpcResponse, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty response from mcp server")
	}
	var resp rpcResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.ID != expectedID {
		return nil, fmt.Errorf("response id %d does not match request id %d", resp.ID, expectedID)
	}
	return &resp, nil
}

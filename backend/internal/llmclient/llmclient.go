// Package llmclient speaks to whatever LLM backend a tenant has configured
// in Settings -> AI Integration (see domain.LLMProvider). It hides the two
// wire formats behind one Client interface: OpenAI's chat/completions shape
// (used by the "openai_compatible", "azure_openai", and "self_hosted" kinds,
// which is why they don't get one adapter each -- see
// service.validateLLMKind) and Anthropic's messages shape (the "anthropic"
// kind). Nothing here has been exercised against a live provider (none was
// available while building this) -- treat it as a solid starting point to
// validate against a real endpoint before relying on it for anything
// customer-facing.
package llmclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/kuruops/kuruops/internal/circuitbreaker"
	"github.com/kuruops/kuruops/internal/httpguard"
)

// tracer's provider is whatever telemetry.Setup registered globally (a
// no-op unless OTEL_EXPORTER_OTLP_ENDPOINT is set) -- see doRequest below,
// this package's one choke point every provider's Complete/CompleteWithTools
// call eventually funnels through.
var tracer = otel.Tracer("kuruops/llmclient")

// breakers fails calls fast while a provider is unhealthy, instead of
// letting every AI analysis run wait out the full defaultTimeout against a
// dead endpoint -- see package circuitbreaker.
//
// Package-level, and keyed by host, for the same reason tracer is: doRequest
// is the one choke point every provider funnels through, and it has no
// per-tenant object to hang state off. Keyed by host rather than by
// provider row so two tenants pointing at the same broken endpoint share
// its breaker -- the endpoint is what's down, not the configuration.
//
// Replaced once at startup by Configure; the default is a disabled
// registry, so tests and any caller that never calls Configure behave
// exactly as they did before the breaker existed.
var breakers atomic.Pointer[circuitbreaker.Registry]

func init() { breakers.Store(circuitbreaker.NewRegistry(circuitbreaker.Config{})) }

// Configure installs the circuit-breaker settings for every LLM call in
// this process. Called once from cmd/* at startup, before any provider is
// used.
func Configure(cfg circuitbreaker.Config) {
	breakers.Store(circuitbreaker.NewRegistry(cfg))
}

// Client sends one system+user prompt pair to an LLM and returns its text
// response, or (via CompleteWithTools) runs one turn of a multi-turn,
// tool-using conversation -- see service.AIAnalysisService's agentic loop
// for the caller that drives multiple CompleteWithTools calls in sequence.
type Client interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
	CompleteWithTools(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (CompletionResult, error)
}

// Tool is a provider-agnostic function definition, built from
// mcpclient.Tool (name/description/JSON Schema) -- both OpenAI's and
// Anthropic's tool-calling wire formats accept a raw JSON Schema object for
// parameters, so InputSchema passes through unmodified.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ToolCall is one function invocation an LLM turn requested. ID is the
// provider's own call identifier (OpenAI's tool_call.id / Anthropic's
// tool_use block id) -- callers must echo it back on the Message that
// carries the result, so the provider can correlate which call it answers.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	// RoleTool carries a tool's result back to the model. Content is the
	// result text; ToolCallID says which prior ToolCall.ID it answers.
	// OpenAI has a native "tool" role for this; Anthropic doesn't (a
	// tool_result there is a content block inside a "user" message) --
	// anthropicClient translates RoleTool messages accordingly.
	RoleTool MessageRole = "tool"
)

// Message is one turn of a provider-agnostic conversation. Exactly one of
// (plain Content) or (ToolCalls, on an assistant message) or (Content +
// ToolCallID, on a tool message) is populated, depending on Role.
type Message struct {
	Role       MessageRole
	Content    string
	ToolCalls  []ToolCall // set on an assistant message that requested tool use
	ToolCallID string     // set on a tool message: which ToolCall.ID this answers
}

// CompletionResult is one LLM turn's output. Text is empty when the model
// only requested tool calls with no accompanying commentary -- that's a
// valid response, not an error.
type CompletionResult struct {
	Text      string
	ToolCalls []ToolCall
}

const defaultTimeout = 60 * time.Second

// New builds the Client for kind, matching the kinds accepted by
// service.validateLLMKind. baseURL is required for openai_compatible,
// azure_openai, and self_hosted (there is no sane default endpoint for any
// of them); anthropic ignores it and always talks to api.anthropic.com,
// consistent with that kind rejecting a custom base_url at save time.
func New(kind string, baseURL *string, apiKey, model string) (Client, error) {
	switch kind {
	case "anthropic":
		return &anthropicClient{
			apiKey: apiKey,
			model:  model,
			http:   &http.Client{Timeout: defaultTimeout},
		}, nil
	case "openai_compatible", "azure_openai", "self_hosted":
		if baseURL == nil || *baseURL == "" {
			return nil, fmt.Errorf("llm provider kind %q requires a base_url", kind)
		}
		return &openAIClient{
			baseURL: *baseURL,
			apiKey:  apiKey,
			model:   model,
			// baseURL is tenant-configured (unlike anthropicClient's fixed
			// api.anthropic.com above), so this goes through httpguard to
			// close the SSRF pivot a plain http.Client would leave open.
			http: httpguard.NewClient(defaultTimeout),
		}, nil
	default:
		return nil, fmt.Errorf("unknown llm provider kind %q", kind)
	}
}

// openAIClient speaks the OpenAI chat/completions wire format, shared by
// every self-hosted/enterprise runtime that exposes an OpenAI-compatible
// endpoint (vLLM, Ollama, Azure OpenAI, etc.) -- see the architecture
// review, "Camada de LLM plugável".
type openAIClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
}

type openAIMessage struct {
	Role       string              `json:"role"`
	Content    string              `json:"content,omitempty"`
	ToolCalls  []openAIToolCallOut `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
}

type openAIToolCallOut struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string                `json:"type"`
	Function openAIToolFunctionDef `json:"function"`
}

type openAIToolFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
		// Carried purely so an empty completion can report why it was
		// empty -- "length" (the answer was truncated to nothing, typical
		// of a reasoning model that spent its whole budget thinking) and
		// "content_filter" are very different problems from a provider
		// that simply returned nothing, and the operator can't tell them
		// apart from a blank analysis.
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *openAIClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqBody, err := json.Marshal(openAIRequest{
		Model: c.model,
		Messages: []openAIMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	respBody, err := doRequest(c.http, httpReq)
	if err != nil {
		return "", err
	}

	var resp openAIResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("llm provider error: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("llm provider returned no choices")
	}
	// An empty completion used to be returned as if it were an answer,
	// which surfaced as an analysis marked "completed" with nothing in it
	// -- no result for the analyst, no error for the operator, and nothing
	// anywhere saying the provider had come back blank.
	if strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return "", emptyCompletionError(resp.Choices[0].FinishReason)
	}
	return resp.Choices[0].Message.Content, nil
}

// emptyCompletionError explains a blank answer in terms of the provider's
// own finish_reason, since the fix differs per cause: a truncated answer
// needs a bigger budget or a smaller prompt, a filtered one needs different
// input, and a bare "stop" with no text usually means the model or endpoint
// is not returning text where an OpenAI-compatible caller expects it.
func emptyCompletionError(finishReason string) error {
	switch finishReason {
	case "length":
		return fmt.Errorf("llm provider returned an empty completion (finish_reason=length): the answer was cut off before any text was produced -- the model's output budget is being consumed before it writes anything")
	case "content_filter":
		return fmt.Errorf("llm provider returned an empty completion (finish_reason=content_filter): the provider suppressed the answer")
	case "":
		return fmt.Errorf("llm provider returned an empty completion with no finish_reason -- the endpoint may not be returning message content where an OpenAI-compatible response is expected")
	default:
		return fmt.Errorf("llm provider returned an empty completion (finish_reason=%s)", finishReason)
	}
}

func (c *openAIClient) CompleteWithTools(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (CompletionResult, error) {
	wireMessages := make([]openAIMessage, 0, len(messages)+1)
	wireMessages = append(wireMessages, openAIMessage{Role: "system", Content: systemPrompt})
	for _, m := range messages {
		wireMessages = append(wireMessages, toOpenAIMessage(m))
	}

	reqBody, err := json.Marshal(openAIRequest{
		Model:    c.model,
		Messages: wireMessages,
		Tools:    toOpenAITools(tools),
	})
	if err != nil {
		return CompletionResult{}, fmt.Errorf("encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return CompletionResult{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	respBody, err := doRequest(c.http, httpReq)
	if err != nil {
		return CompletionResult{}, err
	}

	var resp openAIResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return CompletionResult{}, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return CompletionResult{}, fmt.Errorf("llm provider error: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return CompletionResult{}, fmt.Errorf("llm provider returned no choices")
	}

	msg := resp.Choices[0].Message
	result := CompletionResult{Text: msg.Content}
	for _, tc := range msg.ToolCalls {
		var args map[string]any
		if tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				return CompletionResult{}, fmt.Errorf("decode tool call arguments for %q: %w", tc.Function.Name, err)
			}
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Args: args})
	}
	// Empty text is perfectly normal here as long as the model called a
	// tool -- that turn's output IS the tool call, and the agent loop feeds
	// the result back for the next turn. Empty text with no tool call is
	// the same dead end Complete guards against.
	if strings.TrimSpace(result.Text) == "" && len(result.ToolCalls) == 0 {
		return CompletionResult{}, emptyCompletionError(resp.Choices[0].FinishReason)
	}
	return result, nil
}

func toOpenAIMessage(m Message) openAIMessage {
	switch m.Role {
	case RoleTool:
		return openAIMessage{Role: "tool", Content: m.Content, ToolCallID: m.ToolCallID}
	case RoleAssistant:
		wire := openAIMessage{Role: "assistant", Content: m.Content}
		for _, tc := range m.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Args)
			wire.ToolCalls = append(wire.ToolCalls, openAIToolCallOut{
				ID: tc.ID, Type: "function",
				Function: openAIToolFunction{Name: tc.Name, Arguments: string(argsJSON)},
			})
		}
		return wire
	default:
		return openAIMessage{Role: "user", Content: m.Content}
	}
}

func toOpenAITools(tools []Tool) []openAITool {
	if len(tools) == 0 {
		return nil
	}
	wire := make([]openAITool, len(tools))
	for i, t := range tools {
		wire[i] = openAITool{Type: "function", Function: openAIToolFunctionDef{
			Name: t.Name, Description: t.Description, Parameters: t.InputSchema,
		}}
	}
	return wire
}

// anthropicClient speaks Anthropic's /v1/messages wire format -- distinct
// from OpenAI's in both auth headers (x-api-key + anthropic-version, not
// Authorization: Bearer) and request/response shape (a top-level "system"
// field instead of a system-role message; content is a block array, not a
// single string).
type anthropicClient struct {
	apiKey string
	model  string
	http   *http.Client
}

const (
	anthropicAPIVersion = "2023-06-01"
	// anthropicMaxTokens caps a single analysis response -- generous enough
	// for a triage summary, not a token-budget knob exposed to the tenant.
	anthropicMaxTokens = 2048
)

// anthropicAPIURL is a var, not a const, so tests can point it at a local
// httptest.Server instead of the real Anthropic API.
var anthropicAPIURL = "https://api.anthropic.com/v1/messages"

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *anthropicClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqBody, err := json.Marshal(anthropicRequest{
		Model:     c.model,
		MaxTokens: anthropicMaxTokens,
		System:    systemPrompt,
		Messages:  []anthropicMessage{{Role: "user", Content: userPrompt}},
	})
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	respBody, err := doRequest(c.http, httpReq)
	if err != nil {
		return "", err
	}

	var resp anthropicResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("llm provider error: %s", resp.Error.Message)
	}
	for _, block := range resp.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("llm provider returned no text content")
}

// The types below back CompleteWithTools only -- Complete keeps using the
// plain-string-content anthropicRequest/anthropicMessage/anthropicResponse
// above unmodified, so that method (and its tests) aren't touched by this.
// Content is always an array of blocks here since Anthropic accepts that
// uniformly, sidestepping Go's lack of a JSON union type for "string or
// block array".

type anthropicToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	// text blocks
	Text string `json:"text,omitempty"`
	// tool_use blocks (assistant requesting a call)
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result blocks (feeding a result back, sent as a "user" message)
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicMultiMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicToolsRequest struct {
	Model     string                  `json:"model"`
	MaxTokens int                     `json:"max_tokens"`
	System    string                  `json:"system,omitempty"`
	Tools     []anthropicToolDef      `json:"tools,omitempty"`
	Messages  []anthropicMultiMessage `json:"messages"`
}

type anthropicToolsResponse struct {
	Content []anthropicContentBlock `json:"content"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *anthropicClient) CompleteWithTools(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (CompletionResult, error) {
	wireMessages := make([]anthropicMultiMessage, 0, len(messages))
	for _, m := range messages {
		wireMessages = append(wireMessages, toAnthropicMessage(m))
	}

	reqBody, err := json.Marshal(anthropicToolsRequest{
		Model: c.model, MaxTokens: anthropicMaxTokens, System: systemPrompt,
		Tools: toAnthropicTools(tools), Messages: wireMessages,
	})
	if err != nil {
		return CompletionResult{}, fmt.Errorf("encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(reqBody))
	if err != nil {
		return CompletionResult{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	respBody, err := doRequest(c.http, httpReq)
	if err != nil {
		return CompletionResult{}, err
	}

	var resp anthropicToolsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return CompletionResult{}, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return CompletionResult{}, fmt.Errorf("llm provider error: %s", resp.Error.Message)
	}

	var result CompletionResult
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			result.Text += block.Text
		case "tool_use":
			var args map[string]any
			if len(block.Input) > 0 {
				if err := json.Unmarshal(block.Input, &args); err != nil {
					return CompletionResult{}, fmt.Errorf("decode tool call input for %q: %w", block.Name, err)
				}
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Args: args})
		}
	}
	return result, nil
}

func toAnthropicMessage(m Message) anthropicMultiMessage {
	switch m.Role {
	case RoleTool:
		return anthropicMultiMessage{Role: "user", Content: []anthropicContentBlock{
			{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content},
		}}
	case RoleAssistant:
		var blocks []anthropicContentBlock
		if m.Content != "" {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Args)
			blocks = append(blocks, anthropicContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: argsJSON})
		}
		return anthropicMultiMessage{Role: "assistant", Content: blocks}
	default:
		return anthropicMultiMessage{Role: "user", Content: []anthropicContentBlock{{Type: "text", Text: m.Content}}}
	}
}

func toAnthropicTools(tools []Tool) []anthropicToolDef {
	if len(tools) == 0 {
		return nil
	}
	wire := make([]anthropicToolDef, len(tools))
	for i, t := range tools {
		wire[i] = anthropicToolDef(t)
	}
	return wire
}

func doRequest(client *http.Client, req *http.Request) ([]byte, error) {
	ctx, span := tracer.Start(req.Context(), "llm.request")
	defer span.End()
	req = req.WithContext(ctx)
	span.SetAttributes(attribute.String("http.url", req.URL.Host+req.URL.Path))

	var body []byte
	err := breakers.Load().Do(req.URL.Host, func() error {
		resp, err := client.Do(req)
		if err != nil {
			// A transport error (refused, DNS, timeout) is the clearest
			// signal the provider is unreachable, so it always counts.
			return circuitbreaker.Fault(fmt.Errorf("request failed: %w", err))
		}
		defer resp.Body.Close()

		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return circuitbreaker.Fault(fmt.Errorf("read response: %w", err))
		}
		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
		if resp.StatusCode >= 300 {
			err := fmt.Errorf("llm provider returned %d: %s", resp.StatusCode, string(body))
			// 5xx and 429 say the provider is unhealthy or overloaded.
			// Every other 4xx is our request's fault and would fail
			// identically forever -- counting those would trip the breaker
			// permanently on a wrong API key and bury the one error message
			// that actually tells the operator what to fix.
			if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
				return circuitbreaker.Fault(err)
			}
			return err
		}
		return nil
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return body, nil
}

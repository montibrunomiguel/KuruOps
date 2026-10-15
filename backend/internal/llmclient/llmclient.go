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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

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

// providerError turns an LLM provider's HTTP failure into something an
// analyst can act on.
//
// The whole response body used to become the message, and that message is
// rendered verbatim in the analysis panel -- so a Gemini outage put a
// multi-line JSON blob in front of whoever clicked Analyse, and a wrong
// Base URL produced a bare "llm provider returned 404:" that said nothing
// about which setting was wrong. The statuses below are the ones that
// actually happen, each mapped to the thing to go and change; the raw body
// is kept, truncated, because for anything unrecognised it is the only
// clue there is.
func providerError(status int, body []byte) *ProviderError {
	pe := &ProviderError{Status: status}
	switch status {
	case http.StatusTooManyRequests:
		pe.Message = "the AI provider is rate-limiting requests (429) -- wait a moment and analyse again, or move to a higher quota tier"
	case http.StatusUnauthorized, http.StatusForbidden:
		pe.Message = fmt.Sprintf("the AI provider rejected the credentials (%d) -- check the API key in Settings -> AI Integration", status)
	case http.StatusNotFound:
		pe.Message = "the AI provider returned 404 -- check the Base URL and model name in Settings -> AI Integration; for an OpenAI-compatible endpoint the Base URL is the part before /chat/completions"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		pe.Message = fmt.Sprintf("the AI provider is temporarily unavailable (%d) -- this is usually short-lived, try analysing again in a moment", status)
	default:
		pe.Message = fmt.Sprintf("llm provider returned %d: %s", status, truncateForMessage(body))
	}
	return pe
}

// truncateForMessage caps a provider body so an error that reaches the UI
// stays readable -- some providers answer with kilobytes of HTML.
func truncateForMessage(body []byte) string {
	return truncateBody(body, 300)
}

// truncateBody caps body at max bytes, marking the cut.
func truncateBody(body []byte, max int) string {
	if len(body) <= max {
		return string(body)
	}
	return string(body[:max]) + "... (truncated)"
}

// maxLoggedBodyBytes is larger than what truncateForMessage lets reach the UI:
// the log is where an operator goes to find out why a provider answered 503 or
// 400, so it keeps enough of the body to read a typical JSON error in full.
const maxLoggedBodyBytes = 2000

// The retry policy for a single provider call. A provider's 503 ("model is
// currently experiencing high demand") is the most ordinary failure there is
// and usually clears within seconds, but doRequest used to make exactly one
// attempt, so one blip ended the analysis -- an auto-analysis ended for good,
// with nothing ever retrying it. These are vars, not constants, so a test can
// shrink the waits instead of sleeping through them.
var (
	// retryMaxAttempts is the total number of tries, so 3 means two retries.
	// Kept small on purpose: each try can itself wait out the client timeout,
	// and the analysis queue provides the slow, long-horizon retries (minutes)
	// for a provider that is genuinely down.
	retryMaxAttempts = 3
	retryBaseDelay   = time.Second
	retryMaxDelay    = 8 * time.Second
	// retryAfterCeiling bounds how long a provider's Retry-After is honoured
	// in place. A provider that asks for a minute is not going to be waited out
	// inside one analysis turn; the error is returned instead, and the queue
	// retries at its own, much longer, schedule.
	retryAfterCeiling = 15 * time.Second
)

// retryDelay decides whether a failed attempt should be retried and after how
// long. attempt is the 1-based number of the attempt that just failed.
//
// Only failures that can clear up are retried (ProviderError.Retryable,
// TransportError.Retryable). An open circuit breaker is not: it means "this
// host just failed several times in a row", and hammering it from inside a
// retry loop is exactly what the breaker exists to stop.
func retryDelay(err error, attempt int) (time.Duration, bool) {
	if attempt >= retryMaxAttempts {
		return 0, false
	}
	var pe *ProviderError
	if errors.As(err, &pe) && pe.Retryable() {
		if pe.RetryAfter > retryAfterCeiling {
			return 0, false
		}
		return max(backoff(attempt), pe.RetryAfter), true
	}
	var te *TransportError
	if errors.As(err, &te) && te.Retryable() {
		return backoff(attempt), true
	}
	return 0, false
}

// backoff is exponential (base, 2*base, 4*base ... capped at retryMaxDelay)
// with jitter in [d/2, d], so a burst of analyses that all hit the same 503
// does not retry in lockstep and hit the provider again at the same instant.
func backoff(attempt int) time.Duration {
	d := retryBaseDelay << (attempt - 1)
	if d <= 0 || d > retryMaxDelay {
		d = retryMaxDelay
	}
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + rand.N(half+1)
}

// sleepCtx waits d or until ctx ends, reporting which.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// doRequest sends req and returns the response body, retrying transient
// failures (see retryDelay) with backoff. The request must be repeatable: every
// caller builds it with http.NewRequestWithContext over a bytes.Reader, which
// is what gives it a GetBody to rewind.
func doRequest(client *http.Client, req *http.Request) ([]byte, error) {
	ctx, span := tracer.Start(req.Context(), "llm.request")
	defer span.End()
	req = req.WithContext(ctx)
	span.SetAttributes(attribute.String("http.url", req.URL.Host+req.URL.Path))

	for attempt := 1; ; attempt++ {
		body, err := doRequestOnce(client, req, span, attempt)
		if err == nil {
			span.SetAttributes(attribute.Int("llm.attempts", attempt))
			return body, nil
		}

		delay, retry := retryDelay(err, attempt)
		// A caller that has gone away (cancelled, deadline) is not waiting for
		// an answer any more; do not sleep on its behalf.
		if !retry || ctx.Err() != nil {
			span.SetAttributes(attribute.Int("llm.attempts", attempt))
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		slog.InfoContext(ctx, "retrying llm request after a transient failure",
			"host", req.URL.Host, "attempt", attempt, "max_attempts", retryMaxAttempts, "wait", delay, "error", err)
		if sleepCtx(ctx, delay) != nil {
			// The original failure is the useful one to report, not "context
			// canceled".
			span.RecordError(err)
			return nil, err
		}
	}
}

// doRequestOnce is one attempt: a fresh copy of req (its body rewound), run
// through the circuit breaker for its host.
func doRequestOnce(client *http.Client, req *http.Request, span trace.Span, attempt int) ([]byte, error) {
	attemptReq := req.Clone(req.Context())
	if req.GetBody != nil {
		b, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("rewind request body for attempt %d: %w", attempt, err)
		}
		attemptReq.Body = b
	}

	var body []byte
	err := breakers.Load().Do(attemptReq.URL.Host, func() error {
		resp, err := client.Do(attemptReq)
		if err != nil {
			// A transport error (refused, DNS, timeout) is the clearest
			// signal the provider is unreachable, so it always counts.
			return circuitbreaker.Fault(&TransportError{Err: err})
		}
		defer resp.Body.Close()

		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return circuitbreaker.Fault(&TransportError{Err: fmt.Errorf("read response: %w", err)})
		}
		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
		if resp.StatusCode >= 300 {
			// providerError replaces most statuses with a fixed hint and drops
			// the body, which is the right thing to show an analyst but leaves
			// nothing to diagnose a recurring failure from (a 503 that only
			// ever hits follow-up turns, say). Log what the provider actually
			// said; only the response is logged, never the request or key.
			slog.WarnContext(attemptReq.Context(), "llm provider returned an error response",
				"host", attemptReq.URL.Host, "path", attemptReq.URL.Path, "status", resp.StatusCode, "attempt", attempt,
				"body", truncateBody(body, maxLoggedBodyBytes))
			pe := providerError(resp.StatusCode, body)
			pe.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
			// 5xx and 429 say the provider is unhealthy or overloaded.
			// Every other 4xx is our request's fault and would fail
			// identically forever -- counting those would trip the breaker
			// permanently on a wrong API key and bury the one error message
			// that actually tells the operator what to fix.
			if pe.Retryable() {
				return circuitbreaker.Fault(pe)
			}
			return pe
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return body, nil
}

package router

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cnlanlansky/claude-patch/internal/config"
)

type RequestConversionError struct {
	Err error
}

func (err *RequestConversionError) Error() string {
	return "请求消息转换失败：" + err.Err.Error()
}

func (err *RequestConversionError) Unwrap() error { return err.Err }

type ProxyRequest struct {
	Model          string
	UpstreamModel  string
	ProviderID     string
	SessionID      string
	Protocol       config.Protocol
	AllowFast      bool
	ForceStreaming bool
	Body           map[string]any
	Headers        http.Header
	Context        context.Context
	WebSearch      SearchExecutor
}

type NormalizedToolCall struct {
	ID         string
	Name       string
	Arguments  string
	ServerTool bool
}

type NormalizedResponse struct {
	ID               string
	Text             string
	ReasoningContent string
	Tools            []NormalizedToolCall
	FinishReason     string
	InputTokens      int
	OutputTokens     int
}

type UpstreamResponse struct {
	Status int
	Header http.Header
	Body   []byte
	Stream io.ReadCloser
}

var nonLetters = regexp.MustCompile(`[^a-z]`)

func ProxyMessages(request ProxyRequest, provider config.Provider, client *http.Client) (*UpstreamResponse, error) {
	if provider.Auth != config.AuthNone && strings.TrimSpace(provider.APIKey) == "" {
		return nil, fmt.Errorf("Provider %s 缺少 API key", provider.Label)
	}
	if client == nil {
		client = &http.Client{Timeout: time.Hour}
	}
	protocol := request.Protocol
	if protocol == "" {
		protocol = provider.Protocol
	}
	request.Protocol = protocol
	upstreamModel := request.UpstreamModel
	if upstreamModel == "" {
		upstreamModel = request.Model
	}
	fast := request.AllowFast && stringValue(request.Body["speed"]) == "fast"
	adapter := providerAdapterFor(request.ProviderID)
	prepared, err := adapter.prepare(request, provider, upstreamModel, fast)
	if err != nil {
		var conversionErr *RequestConversionError
		if errors.As(err, &conversionErr) {
			return nil, err
		}
		return nil, &RequestConversionError{Err: err}
	}
	payloadBytes, err := json.Marshal(prepared.Payload)
	if err != nil {
		return nil, err
	}
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL(provider.BaseURL, prepared.Path), bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, err
	}
	upstream.Header = adapter.headers(provider, request.Headers, fast, protocol, request.ProviderID, request.SessionID)
	response, err := client.Do(upstream)
	if err != nil {
		return nil, err
	}
	result := &UpstreamResponse{Status: response.StatusCode, Header: cloneResponseHeaders(response.Header)}
	serverSearch := hasServerWebSearch(request.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 || protocol == config.AnthropicMessages && !serverSearch {
		result.Stream = response.Body
		return result, nil
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBodyBytes {
		return nil, errors.New("upstream response body too large")
	}
	result.Body = raw
	normalized, err := normalizeUpstream(raw, response.Header.Get("content-type"), protocol, prepared.Aliases, prepared.PreserveReasoningContent)
	if err != nil {
		return result, nil
	}
	if serverSearch {
		for index := range normalized.Tools {
			if isWebSearchTool(normalized.Tools[index].Name) {
				normalized.Tools[index].ServerTool = true
			}
		}
	}
	if protocol == config.AnthropicMessages && !hasServerTool(normalized.Tools) {
		return result, nil
	}
	searches := resolveSearches(ctx, normalized, request.WebSearch)
	stream := boolValue(request.Body["stream"], true)
	if stream {
		result.Body = anthropicStream(normalized, request.Model, searches, prepared.PreserveReasoningContent)
		result.Header = http.Header{"Content-Type": []string{"text/event-stream"}, "Cache-Control": []string{"no-cache"}}
	} else {
		result.Body, _ = json.Marshal(anthropicResponse(normalized, request.Model, searches, prepared.PreserveReasoningContent))
		result.Header = http.Header{"Content-Type": []string{"application/json"}}
	}
	return result, nil
}

func upstreamURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") && strings.HasPrefix(path, "/v1/") {
		return base + strings.TrimPrefix(path, "/v1")
	}
	return base + path
}

func anthropicBody(body map[string]any, model string, allowFast, normalizeTools bool) (map[string]any, error) {
	output := cloneMap(body)
	output["model"] = model
	if normalizeTools {
		if messages, ok := body["messages"].([]any); ok {
			converted, err := anthropicMessagesForGo(messages)
			if err != nil {
				return nil, err
			}
			output["messages"] = converted
		}
		if tools, ok := body["tools"].([]any); ok {
			output["tools"] = anthropicTools(tools)
			choice := mapValue(body["tool_choice"])
			if typeValue := stringValue(choice["type"]); typeValue == "tool" || typeValue == "any" {
				output["tool_choice"] = map[string]any{"type": "auto"}
			}
		}
	}
	if !allowFast && stringValue(output["speed"]) == "fast" {
		delete(output, "speed")
	}
	if !allowFast {
		if betas, ok := output["betas"].([]any); ok {
			var filtered []any
			for _, beta := range betas {
				if stringValue(beta) != "fast-mode-2026-02-01" {
					filtered = append(filtered, beta)
				}
			}
			output["betas"] = filtered
		}
	}
	return output, nil
}

func toChatRequest(body map[string]any, model string, allowFast, forceStreaming bool, aliases map[string]string) (map[string]any, error) {
	return toChatRequestWithReasoning(body, model, allowFast, forceStreaming, aliases, false)
}

func toChatRequestWithReasoning(body map[string]any, model string, allowFast, forceStreaming bool, aliases map[string]string, includeReasoning ...bool) (map[string]any, error) {
	withReasoning := len(includeReasoning) > 0 && includeReasoning[0]
	messages, err := anthropicMessagesWithReasoning(body, withReasoning)
	if err != nil {
		return nil, &RequestConversionError{Err: err}
	}
	request := map[string]any{
		"model": model, "messages": messages,
		"stream": forceStreaming || boolValue(body["stream"], true),
	}
	if forceStreaming {
		request["stream_options"] = map[string]any{"include_usage": true}
	}
	copyIfPresent(request, body, "max_tokens", "temperature", "top_p")
	if effort, ok := mapValue(body["output_config"])["effort"]; ok {
		request["reasoning_effort"] = effort
	}
	if allowFast && stringValue(body["speed"]) == "fast" {
		request["service_tier"] = "priority"
	}
	if tools := toolsForOpenAI(body, aliases); len(tools) > 0 {
		request["tools"] = tools
	}
	if choice := toolChoice(body, false, aliases); choice != nil {
		request["tool_choice"] = choice
	}
	return request, nil
}

func toResponsesRequest(body map[string]any, model string, allowFast, forceStreaming bool, aliases map[string]string) (map[string]any, error) {
	input, err := anthropicResponsesInput(body, aliases)
	if err != nil {
		return nil, &RequestConversionError{Err: err}
	}
	request := map[string]any{
		"model": model, "input": input,
		"stream": forceStreaming || boolValue(body["stream"], true),
	}
	if value, ok := body["max_tokens"]; ok {
		request["max_output_tokens"] = value
	}
	if effort, ok := mapValue(body["output_config"])["effort"]; ok {
		reasoning := cloneMap(mapValue(body["reasoning"]))
		reasoning["effort"] = effort
		request["reasoning"] = reasoning
	}
	if allowFast && stringValue(body["speed"]) == "fast" {
		request["service_tier"] = "priority"
	}
	if tools := toolsForOpenAI(body, aliases); len(tools) > 0 {
		flat := make([]any, 0, len(tools))
		for _, tool := range tools {
			function := mapValue(mapValue(tool)["function"])
			flat = append(flat, map[string]any{"type": "function", "name": function["name"], "description": function["description"], "parameters": function["parameters"]})
		}
		request["tools"] = flat
	}
	if choice := toolChoice(body, true, aliases); choice != nil {
		request["tool_choice"] = choice
	}
	return request, nil
}

type toolCallLedger struct {
	pending map[string]struct{}
}

func newToolCallLedger() toolCallLedger {
	return toolCallLedger{pending: make(map[string]struct{})}
}

func validateUseIDs(groups ...[]map[string]any) error {
	ids := make(map[string]struct{})
	for _, uses := range groups {
		for _, use := range uses {
			id := stringValue(use["id"])
			if id == "" {
				return errors.New("tool_use 缺少 id")
			}
			if _, exists := ids[id]; exists {
				return fmt.Errorf("tool_use id 重复：%s", id)
			}
			ids[id] = struct{}{}
		}
	}
	return nil
}

func (ledger *toolCallLedger) addUses(uses []map[string]any) error {
	if err := validateUseIDs(uses); err != nil {
		return err
	}
	for _, use := range uses {
		id := stringValue(use["id"])
		if _, exists := ledger.pending[id]; exists {
			return fmt.Errorf("tool_use id 未闭合前再次出现：%s", id)
		}
	}
	for _, use := range uses {
		ledger.pending[stringValue(use["id"])] = struct{}{}
	}
	return nil
}

func resultID(result map[string]any) string {
	return stringOr(result["tool_use_id"], stringValue(result["tool_call_id"]))
}

func (ledger *toolCallLedger) consumeResults(results []map[string]any) error {
	ids := make(map[string]struct{}, len(results))
	for _, result := range results {
		id := resultID(result)
		if id == "" {
			return errors.New("tool_result 缺少 tool_use_id")
		}
		if _, exists := ids[id]; exists {
			return fmt.Errorf("tool_result id 重复：%s", id)
		}
		if _, exists := ledger.pending[id]; !exists {
			return fmt.Errorf("tool_result 未匹配 tool_use：%s", id)
		}
		ids[id] = struct{}{}
	}
	for id := range ids {
		delete(ledger.pending, id)
	}
	return nil
}

func (ledger *toolCallLedger) hasPending() bool {
	return len(ledger.pending) > 0
}

func (ledger *toolCallLedger) requireComplete() error {
	if len(ledger.pending) == 0 {
		return nil
	}
	ids := make([]string, 0, len(ledger.pending))
	for id := range ledger.pending {
		ids = append(ids, id)
	}
	return fmt.Errorf("tool_use 未收到对应 tool_result：%s", strings.Join(ids, ","))
}

func anthropicMessages(body map[string]any) ([]any, error) {
	return anthropicMessagesWithReasoning(body, false)
}

func anthropicMessagesWithReasoning(body map[string]any, includeReasoning bool) ([]any, error) {
	var output []any
	ledger := newToolCallLedger()
	if system, exists := body["system"]; exists {
		output = append(output, map[string]any{"role": "system", "content": textFromContent(system)})
	}
	for _, raw := range sliceValue(body["messages"]) {
		message := mapValue(raw)
		role := stringValue(message["role"])
		content := message["content"]
		if role == "assistant" {
			if ledger.hasPending() {
				return nil, errors.New("tool_calls 后缺少 tool message")
			}
			if _, ok := content.(string); ok {
				output = append(output, map[string]any{"role": "assistant", "content": content})
				continue
			}
			contentBlocks := mapsFromSlice(content)
			reasoning := ""
			if includeReasoning {
				for _, block := range contentBlocks {
					switch stringValue(block["type"]) {
					case "thinking":
						if stringValue(block["signature"]) != "" {
							return nil, errors.New("OpenCode Free 不支持带 signature 的 thinking")
						}
						reasoning += stringValue(block["thinking"])
					case "redacted_thinking":
						return nil, errors.New("OpenCode Free 不支持 redacted_thinking")
					}
				}
			}
			blocks := classifyToolBlocks(contentBlocks)
			if len(blocks.clientResults) > 0 {
				return nil, errors.New("assistant 消息不能包含 tool_result")
			}
			if err := validateUseIDs(blocks.clientUses, blocks.serverUses); err != nil {
				return nil, err
			}
			if err := validateServerResults(blocks.serverUses, blocks.serverResults); err != nil {
				return nil, err
			}
			if len(blocks.clientUses) > 0 {
				if err := ledger.addUses(blocks.clientUses); err != nil {
					return nil, err
				}
			}
			if len(blocks.uses) > 0 {
				calls := make([]any, 0, len(blocks.uses))
				for _, block := range blocks.uses {
					name := stringValue(block["name"])
					if name == "" {
						return nil, fmt.Errorf("tool_use %s 缺少 name", stringValue(block["id"]))
					}
					arguments, _ := json.Marshal(valueOr(block["input"], map[string]any{}))
					calls = append(calls, map[string]any{"id": block["id"], "type": "function", "function": map[string]any{"name": name, "arguments": string(arguments)}})
				}
				assistant := map[string]any{"role": "assistant", "content": nullableText(content), "tool_calls": calls}
				if includeReasoning {
					assistant["reasoning_content"] = reasoning
				}
				output = append(output, assistant)
			}
			for _, result := range blocks.serverResults {
				output = append(output, map[string]any{"role": "tool", "tool_call_id": resultID(result), "content": toolResultText(result["content"])})
			}
			if len(blocks.uses) > 0 || len(blocks.serverResults) > 0 {
				continue
			}
			assistant := map[string]any{"role": "assistant", "content": textFromContent(content)}
			if includeReasoning {
				assistant["reasoning_content"] = reasoning
			}
			output = append(output, assistant)
			continue
		}
		if role == "tool" {
			id := resultID(message)
			if err := ledger.consumeResults([]map[string]any{message}); err != nil {
				return nil, err
			}
			output = append(output, map[string]any{"role": "tool", "tool_call_id": id, "content": toolResultText(content)})
			continue
		}
		if role == "system" {
			if ledger.hasPending() {
				return nil, errors.New("tool_calls 后不能插入 system 消息")
			}
			output = append(output, map[string]any{"role": "system", "content": textFromContent(content)})
			continue
		}
		if role != "user" {
			role = "user"
		}
		if text, ok := content.(string); ok {
			if ledger.hasPending() {
				return nil, errors.New("tool_calls 后缺少 tool message")
			}
			output = append(output, map[string]any{"role": role, "content": text})
			continue
		}
		blocks := classifyToolBlocks(mapsFromSlice(content))
		if len(blocks.uses) > 0 {
			return nil, errors.New("user 消息不能包含 tool_use")
		}
		if len(blocks.serverResults) > 0 {
			return nil, errors.New("user 消息不能包含 server tool_result")
		}
		if len(blocks.clientResults) > 0 {
			if err := ledger.consumeResults(blocks.clientResults); err != nil {
				return nil, err
			}
			for _, result := range blocks.clientResults {
				output = append(output, map[string]any{"role": "tool", "tool_call_id": resultID(result), "content": toolResultText(result["content"])})
			}
			if text := textFromContent(content); text != "" {
				output = append(output, map[string]any{"role": "user", "content": text})
			}
			continue
		}
		if ledger.hasPending() {
			return nil, errors.New("tool_calls 后缺少 tool message")
		}
		output = append(output, map[string]any{"role": role, "content": textFromContent(content)})
	}
	if err := ledger.requireComplete(); err != nil {
		return nil, err
	}
	return output, nil
}

func anthropicResponsesInput(body map[string]any, aliases map[string]string) ([]any, error) {
	var output []any
	ledger := newToolCallLedger()
	if system, exists := body["system"]; exists {
		output = append(output, map[string]any{"role": "system", "content": textFromContent(system)})
	}
	for _, raw := range sliceValue(body["messages"]) {
		message := mapValue(raw)
		role := "user"
		if message["role"] == "assistant" {
			role = "assistant"
		}
		content := message["content"]
		if text, ok := content.(string); ok {
			if ledger.hasPending() {
				return nil, errors.New("tool_calls 后缺少 tool message")
			}
			output = append(output, map[string]any{"role": role, "content": text})
			continue
		}
		blocks := classifyToolBlocks(mapsFromSlice(content))
		if role == "assistant" {
			if len(blocks.clientResults) > 0 {
				return nil, errors.New("assistant 消息不能包含 tool_result")
			}
			if err := validateUseIDs(blocks.clientUses, blocks.serverUses); err != nil {
				return nil, err
			}
			if err := validateServerResults(blocks.serverUses, blocks.serverResults); err != nil {
				return nil, err
			}
			if len(blocks.clientUses) > 0 {
				if err := ledger.addUses(blocks.clientUses); err != nil {
					return nil, err
				}
			}
			if len(blocks.uses) > 0 {
				if text := textFromContent(content); text != "" {
					output = append(output, map[string]any{"role": "assistant", "content": text})
				}
				for _, block := range blocks.uses {
					id := stringValue(block["id"])
					name := stringValue(block["name"])
					if name == "" {
						return nil, fmt.Errorf("tool_use %s 缺少 name", id)
					}
					arguments, _ := json.Marshal(valueOr(block["input"], map[string]any{}))
					output = append(output, map[string]any{"type": "function_call", "call_id": id, "name": upstreamToolName(name, aliases), "arguments": string(arguments)})
				}
			}
			for _, result := range blocks.serverResults {
				output = append(output, map[string]any{"type": "function_call_output", "call_id": resultID(result), "output": toolResultText(result["content"])})
			}
			if len(blocks.uses) > 0 || len(blocks.serverResults) > 0 {
				continue
			}
			if ledger.hasPending() {
				return nil, errors.New("tool_calls 后缺少 tool message")
			}
			output = append(output, map[string]any{"role": "assistant", "content": textFromContent(content)})
			continue
		}
		if len(blocks.uses) > 0 || len(blocks.serverResults) > 0 {
			return nil, errors.New("user 消息不能包含 tool_use")
		}
		if len(blocks.clientResults) > 0 {
			if err := ledger.consumeResults(blocks.clientResults); err != nil {
				return nil, err
			}
			for _, result := range blocks.clientResults {
				output = append(output, map[string]any{"type": "function_call_output", "call_id": resultID(result), "output": toolResultText(result["content"])})
			}
			if text := textFromContent(content); text != "" {
				output = append(output, map[string]any{"role": "user", "content": text})
			}
			continue
		}
		if ledger.hasPending() {
			return nil, errors.New("tool_calls 后缺少 tool message")
		}
		output = append(output, map[string]any{"role": role, "content": textFromContent(content)})
	}
	if err := ledger.requireComplete(); err != nil {
		return nil, err
	}
	return output, nil
}

func anthropicMessagesForGo(messages []any) ([]any, error) {
	var output []any
	ledger := newToolCallLedger()
	for _, raw := range messages {
		message := mapValue(raw)
		role := stringValue(message["role"])
		content := message["content"]
		if role == "assistant" {
			if ledger.hasPending() {
				return nil, errors.New("tool_calls 后缺少 tool message")
			}
			blocks := classifyToolBlocks(mapsFromSlice(content))
			if err := validateUseIDs(blocks.clientUses, blocks.serverUses); err != nil {
				return nil, err
			}
			if err := validateServerResults(blocks.serverUses, blocks.serverResults); err != nil {
				return nil, err
			}
			if len(blocks.clientResults) > 0 {
				return nil, errors.New("assistant 消息不能包含 tool_result")
			}
			if len(blocks.clientUses) > 0 {
				if err := ledger.addUses(blocks.clientUses); err != nil {
					return nil, err
				}
			}
			if len(blocks.uses) > 0 {
				copy := cloneMap(message)
				clean := make([]any, 0, len(blocks.uses))
				for _, block := range blocks.uses {
					item := cloneMap(block)
					if stringValue(item["type"]) == "server_tool_use" {
						item["type"] = "tool_use"
					}
					clean = append(clean, item)
				}
				copy["content"] = clean
				output = append(output, copy)
			}
			for _, result := range blocks.serverResults {
				output = append(output, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": resultID(result), "content": toolResultText(result["content"])}}})
			}
			if len(blocks.uses) > 0 || len(blocks.serverResults) > 0 {
				continue
			}
			output = append(output, message)
			continue
		}
		blocks, ok := content.([]any)
		if !ok {
			if ledger.hasPending() {
				return nil, errors.New("tool_calls 后缺少 tool message")
			}
			output = append(output, message)
			continue
		}
		classified := classifyToolBlocks(mapsFromSlice(blocks))
		if len(classified.uses) > 0 || len(classified.serverResults) > 0 {
			return nil, errors.New("user 消息不能包含 tool_use")
		}
		if len(classified.clientResults) > 0 {
			if err := ledger.consumeResults(classified.clientResults); err != nil {
				return nil, err
			}
			for _, result := range classified.clientResults {
				output = append(output, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": resultID(result), "content": toolResultText(result["content"])}}})
			}
			if text := textFromContent(content); text != "" {
				output = append(output, map[string]any{"role": "user", "content": text})
			}
			continue
		}
		if ledger.hasPending() {
			return nil, errors.New("tool_calls 后缺少 tool message")
		}
		output = append(output, message)
	}
	if err := ledger.requireComplete(); err != nil {
		return nil, err
	}
	return output, nil
}

func anthropicTools(tools []any) []any {
	output := make([]any, 0, len(tools))
	for _, raw := range tools {
		value := mapValue(raw)
		typeName, name := stringValue(value["type"]), stringValue(value["name"])
		if (!strings.HasPrefix(typeName, "web_search_") && !strings.HasPrefix(typeName, "web_fetch_")) || name == "" {
			output = append(output, value)
			continue
		}
		schema := emptyToolSchema()
		if name == "web_search" {
			schema = webSearchSchema()
		} else if name == "web_fetch" {
			schema = webFetchSchema()
		}
		tool := map[string]any{"name": name, "input_schema": schema}
		if description := stringValue(value["description"]); description != "" {
			tool["description"] = description
		}
		output = append(output, tool)
	}
	return output
}

func toolsForOpenAI(body map[string]any, aliases map[string]string) []any {
	var tools []any
	for _, raw := range sliceValue(body["tools"]) {
		value := mapValue(raw)
		function := mapValue(value["function"])
		source := function
		if len(source) == 0 {
			source = value
		}
		name := stringValue(function["name"])
		if name == "" {
			name = stringValue(value["name"])
		}
		if name == "" {
			continue
		}
		schema := mapValue(valueOr(source["parameters"], value["input_schema"]))
		if stringValue(schema["type"]) != "object" {
			typeName := stringValue(value["type"])
			if name == "web_search" || strings.HasPrefix(typeName, "web_search_") {
				schema = webSearchSchema()
			} else if name == "web_fetch" || strings.HasPrefix(typeName, "web_fetch_") {
				schema = webFetchSchema()
			} else {
				schema = emptyToolSchema()
			}
		}
		description := stringValue(source["description"])
		if description == "" {
			if name == "web_search" {
				description = "Search the web for current information."
			} else if name == "web_fetch" {
				description = "Fetch content from a URL."
			} else {
				description = "Execute the " + name + " tool."
			}
		}
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": upstreamToolName(name, aliases), "description": description, "parameters": schema}})
	}
	return tools
}

func toolChoice(body map[string]any, responses bool, aliases map[string]string) any {
	choice := mapValue(body["tool_choice"])
	typeName := stringValue(choice["type"])
	if typeName == "auto" || typeName == "none" {
		return typeName
	}
	if typeName == "any" {
		return "required"
	}
	if typeName == "tool" && stringValue(choice["name"]) != "" {
		name := upstreamToolName(stringValue(choice["name"]), aliases)
		if responses {
			return map[string]any{"type": "function", "name": name}
		}
		return map[string]any{"type": "function", "function": map[string]any{"name": name}}
	}
	return nil
}

func normalizeUpstream(raw []byte, contentType string, protocol config.Protocol, aliases map[string]string, includeReasoning ...bool) (NormalizedResponse, error) {
	preserveReasoning := len(includeReasoning) > 0 && includeReasoning[0]
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		if protocol == config.AnthropicMessages {
			return normalizeAnthropicStream(raw), nil
		}
		return normalizeOpenAIStreamWithReasoning(raw, protocol, aliases, preserveReasoning), nil
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return NormalizedResponse{}, err
	}
	if protocol == config.AnthropicMessages {
		return normalizeAnthropic(body), nil
	}
	if protocol == config.OpenAIResponses {
		return normalizeResponses(body, aliases), nil
	}
	return normalizeChatWithReasoning(body, preserveReasoning), nil
}

func normalizeChat(body map[string]any) NormalizedResponse {
	return normalizeChatWithReasoning(body, false)
}

func normalizeChatWithReasoning(body map[string]any, includeReasoning bool) NormalizedResponse {
	choice := mapValue(first(sliceValue(body["choices"])))
	message := mapValue(valueOr(choice["message"], choice["delta"]))
	var tools []NormalizedToolCall
	for _, raw := range sliceValue(message["tool_calls"]) {
		value := mapValue(raw)
		function := mapValue(value["function"])
		if name := stringValue(function["name"]); name != "" {
			tools = append(tools, NormalizedToolCall{ID: stringOr(value["id"], "tool_call"), Name: name, Arguments: stringOr(function["arguments"], "{}")})
		}
	}
	input, output := usage(body["usage"])
	result := NormalizedResponse{ID: stringOr(body["id"], "router_message"), Text: textFromContent(message["content"]), Tools: tools, FinishReason: stringValue(choice["finish_reason"]), InputTokens: input, OutputTokens: output}
	if includeReasoning {
		result.ReasoningContent = stringValue(message["reasoning_content"])
		if result.ReasoningContent == "" {
			result.ReasoningContent = stringValue(mapValue(choice["delta"])["reasoning_content"])
		}
	}
	return result
}

func normalizeResponses(body map[string]any, aliases map[string]string) NormalizedResponse {
	var text string
	var tools []NormalizedToolCall
	for _, raw := range sliceValue(body["output"]) {
		item := mapValue(raw)
		if item["type"] == "message" {
			text += textFromContent(item["content"])
		} else if item["type"] == "output_text" {
			text += stringValue(item["text"])
		} else if item["type"] == "function_call" && stringValue(item["name"]) != "" {
			tools = append(tools, NormalizedToolCall{ID: stringOr(valueOr(item["call_id"], item["id"]), "tool_call"), Name: clientToolName(stringValue(item["name"]), aliases), Arguments: stringOr(item["arguments"], "{}")})
		}
	}
	input, output := usage(body["usage"])
	if text == "" {
		text = textFromContent(body["output_text"])
	}
	finish := "stop"
	if len(tools) > 0 {
		finish = "tool_calls"
	}
	return NormalizedResponse{ID: stringOr(body["id"], "router_message"), Text: text, Tools: tools, FinishReason: finish, InputTokens: input, OutputTokens: output}
}

func normalizeAnthropic(body map[string]any) NormalizedResponse {
	var tools []NormalizedToolCall
	for _, raw := range sliceValue(body["content"]) {
		block := mapValue(raw)
		if block["type"] == "tool_use" || block["type"] == "server_tool_use" {
			arguments, _ := json.Marshal(valueOr(block["input"], map[string]any{}))
			tools = append(tools, NormalizedToolCall{ID: stringOr(block["id"], "tool_call"), Name: stringOr(block["name"], "tool"), Arguments: string(arguments)})
		}
	}
	input, output := usage(body["usage"])
	return NormalizedResponse{ID: stringOr(body["id"], "router_message"), Text: textFromContent(body["content"]), Tools: tools, FinishReason: stringValue(body["stop_reason"]), InputTokens: input, OutputTokens: output}
}

func normalizeOpenAIStream(raw []byte, protocol config.Protocol, aliases map[string]string) NormalizedResponse {
	return normalizeOpenAIStreamWithReasoning(raw, protocol, aliases, false)
}

func normalizeOpenAIStreamWithReasoning(raw []byte, protocol config.Protocol, aliases map[string]string, includeReasoning bool) NormalizedResponse {
	records := parseSSE(raw)
	normalized := NormalizedResponse{ID: "router_message"}
	tools := map[string]*NormalizedToolCall{}
	var toolOrder []string
	for _, frame := range records {
		data := frame.data
		response := mapValue(data["response"])
		normalized.ID = stringOr(valueOr(data["id"], response["id"]), normalized.ID)
		choice := mapValue(first(sliceValue(data["choices"])))
		delta := mapValue(choice["delta"])
		if includeReasoning {
			normalized.ReasoningContent += stringValue(delta["reasoning_content"])
			if reasoning := stringValue(mapValue(choice["message"])["reasoning_content"]); reasoning != "" && normalized.ReasoningContent == "" {
				normalized.ReasoningContent = reasoning
			}
		}
		normalized.Text += textFromContent(delta["content"])
		if reason := stringValue(choice["finish_reason"]); reason != "" {
			normalized.FinishReason = reason
		}
		for _, rawTool := range sliceValue(delta["tool_calls"]) {
			value := mapValue(rawTool)
			function := mapValue(value["function"])
			key := stringOr(fmt.Sprint(valueOr(value["index"], valueOr(value["id"], ""))), strconv.Itoa(len(tools)))
			tool := tools[key]
			if tool == nil {
				tool = &NormalizedToolCall{ID: stringOr(value["id"], "tool_call_"+key)}
				tools[key] = tool
				toolOrder = append(toolOrder, key)
			}
			if name := stringValue(function["name"]); name != "" {
				tool.Name = name
			}
			tool.Arguments += stringValue(function["arguments"])
		}
		if frame.event == "response.output_text.delta" {
			normalized.Text += stringValue(data["delta"])
		}
		if strings.HasPrefix(frame.event, "response.function_call_arguments.") || frame.event == "response.output_item.added" || frame.event == "response.output_item.done" {
			item := mapValue(data["item"])
			key := stringOr(valueOr(data["item_id"], valueOr(item["id"], item["call_id"])), strconv.Itoa(len(tools)))
			tool := tools[key]
			if tool == nil {
				tool = &NormalizedToolCall{ID: stringOr(valueOr(item["call_id"], item["id"]), key)}
				tools[key] = tool
				toolOrder = append(toolOrder, key)
			}
			if name := stringValue(item["name"]); name != "" {
				tool.Name = name
			}
			if arguments := stringValue(valueOr(data["arguments"], item["arguments"])); arguments != "" {
				tool.Arguments = arguments
			} else {
				tool.Arguments += stringValue(data["delta"])
			}
		}
		input, output := usage(valueOr(data["usage"], response["usage"]))
		if input != 0 {
			normalized.InputTokens = input
		}
		if output != 0 {
			normalized.OutputTokens = output
		}
		if frame.event == "response.completed" {
			normalized.FinishReason = "stop"
		}
	}
	for _, key := range toolOrder {
		tool := tools[key]
		if tool.Name != "" {
			tool.Name = clientToolName(tool.Name, aliases)
			normalized.Tools = append(normalized.Tools, *tool)
		}
	}
	if normalized.FinishReason == "" && protocol == config.OpenAIResponses {
		normalized.FinishReason = "stop"
	}
	return normalized
}

func normalizeAnthropicStream(raw []byte) NormalizedResponse {
	type block struct{ kind, id, name, text, input string }
	blocks := map[string]*block{}
	var blockOrder []string
	normalized := NormalizedResponse{ID: "router_message"}
	for _, frame := range parseSSE(raw) {
		data := frame.data
		if data["type"] == "message_start" {
			message := mapValue(data["message"])
			normalized.ID = stringOr(message["id"], normalized.ID)
			normalized.InputTokens, normalized.OutputTokens = usage(message["usage"])
		}
		key := fmt.Sprint(valueOr(data["index"], len(blocks)))
		if data["type"] == "content_block_start" {
			value := mapValue(data["content_block"])
			if _, exists := blocks[key]; !exists {
				blockOrder = append(blockOrder, key)
			}
			blocks[key] = &block{kind: stringOr(value["type"], "text"), id: stringValue(value["id"]), name: stringValue(value["name"]), text: stringValue(value["text"])}
		}
		if data["type"] == "content_block_delta" {
			value := mapValue(data["delta"])
			if current := blocks[key]; current != nil {
				if value["type"] == "text_delta" {
					current.text += stringValue(value["text"])
				}
				if value["type"] == "input_json_delta" {
					current.input += stringValue(value["partial_json"])
				}
			}
		}
		if data["type"] == "message_delta" {
			normalized.FinishReason = stringValue(mapValue(data["delta"])["stop_reason"])
			input, output := usage(data["usage"])
			if input != 0 {
				normalized.InputTokens = input
			}
			if output != 0 {
				normalized.OutputTokens = output
			}
		}
	}
	for _, key := range blockOrder {
		current := blocks[key]
		if current.kind == "text" {
			normalized.Text += current.text
		}
		if current.kind == "tool_use" || current.kind == "server_tool_use" {
			normalized.Tools = append(normalized.Tools, NormalizedToolCall{ID: stringOr(current.id, "tool_call"), Name: stringOr(current.name, "tool"), Arguments: stringOr(current.input, "{}")})
		}
	}
	return normalized
}

func anthropicResponse(normalized NormalizedResponse, model string, searches []searchResolution, includeReasoning ...bool) map[string]any {
	var content []any
	if len(includeReasoning) > 0 && includeReasoning[0] && normalized.ReasoningContent != "" {
		content = append(content, map[string]any{"type": "thinking", "thinking": normalized.ReasoningContent})
	}
	if normalized.Text != "" {
		content = append(content, map[string]any{"type": "text", "text": normalized.Text})
	}
	searchByID := map[string]searchResolution{}
	for _, search := range searches {
		searchByID[search.ID] = search
	}
	hasRegular := false
	for _, tool := range normalized.Tools {
		var input map[string]any
		_ = json.Unmarshal([]byte(tool.Arguments), &input)
		if search, ok := searchByID[tool.ID]; ok {
			content = append(content, map[string]any{"type": "server_tool_use", "id": tool.ID, "name": "web_search", "input": input})
			var result any
			if search.Error != "" {
				result = map[string]any{"type": "web_search_tool_result_error", "error_code": search.ErrorCode}
			} else if search.SearchResultContent != nil {
				result = search.SearchResultContent
			} else {
				result = []any{}
			}
			content = append(content, map[string]any{"type": "web_search_tool_result", "tool_use_id": tool.ID, "content": result})
		} else {
			hasRegular = true
			content = append(content, map[string]any{"type": "tool_use", "id": tool.ID, "name": tool.Name, "input": input})
		}
	}
	stop := "end_turn"
	if hasRegular {
		stop = "tool_use"
	} else if len(searches) == 0 && normalized.FinishReason == "length" {
		stop = "max_tokens"
	}
	return map[string]any{"id": normalized.ID, "type": "message", "role": "assistant", "model": model, "content": content, "stop_reason": stop, "stop_sequence": nil, "usage": map[string]any{"input_tokens": normalized.InputTokens, "output_tokens": normalized.OutputTokens}}
}

func anthropicStream(normalized NormalizedResponse, model string, searches []searchResolution, includeReasoning ...bool) []byte {
	message := anthropicResponse(normalized, model, searches, includeReasoning...)
	blocks := sliceValue(message["content"])
	var output bytes.Buffer
	writeSSE(&output, "message_start", map[string]any{"type": "message_start", "message": mergeMap(message, map[string]any{"content": []any{}, "stop_reason": nil})})
	for index, raw := range blocks {
		block := mapValue(raw)
		typeName := stringValue(block["type"])
		var start any
		if typeName == "text" {
			start = map[string]any{"type": "text", "text": ""}
		} else if typeName == "thinking" {
			start = map[string]any{"type": "thinking", "thinking": ""}
		} else if typeName == "tool_use" || typeName == "server_tool_use" {
			start = map[string]any{"type": typeName, "id": block["id"], "name": block["name"], "input": map[string]any{}}
		} else {
			start = block
		}
		writeSSE(&output, "content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": start})
		if typeName == "text" {
			writeSSE(&output, "content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": block["text"]}})
		}
		if typeName == "thinking" {
			writeSSE(&output, "content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "thinking_delta", "thinking": block["thinking"]}})
		}
		if typeName == "tool_use" || typeName == "server_tool_use" {
			bytes, _ := json.Marshal(block["input"])
			writeSSE(&output, "content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(bytes)}})
		}
		writeSSE(&output, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
	}
	writeSSE(&output, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": message["stop_reason"], "stop_sequence": nil}, "usage": message["usage"]})
	writeSSE(&output, "message_stop", map[string]any{"type": "message_stop"})
	return output.Bytes()
}

type sseFrame struct {
	event string
	data  map[string]any
}

func parseSSE(raw []byte) []sseFrame {
	var records []sseFrame
	event := "message"
	var data []string
	flush := func() {
		text := strings.TrimSpace(strings.Join(data, "\n"))
		data = nil
		if text != "" && text != "[DONE]" {
			var value map[string]any
			if json.Unmarshal([]byte(text), &value) == nil {
				records = append(records, sseFrame{event, value})
			}
		}
		event = "message"
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			flush()
		} else if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	return records
}
func writeSSE(output *bytes.Buffer, event string, value any) {
	data, _ := json.Marshal(value)
	fmt.Fprintf(output, "event: %s\ndata: %s\n\n", event, data)
}

func randomHex(size int) string {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(bytes)
}

func cloneResponseHeaders(source http.Header) http.Header {
	result := source.Clone()
	for _, value := range source.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			result.Del(strings.TrimSpace(name))
		}
	}
	for name := range result {
		if hopByHopHeader(name) {
			result.Del(name)
		}
	}
	return result
}
func splitHeader(value string) []string {
	var output []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			output = append(output, item)
		}
	}
	return output
}
func cloneMap(value map[string]any) map[string]any {
	output := make(map[string]any, len(value))
	for key, item := range value {
		output[key] = item
	}
	return output
}
func mergeMap(left, right map[string]any) map[string]any {
	output := cloneMap(left)
	for key, value := range right {
		output[key] = value
	}
	return output
}
func mapValue(value any) map[string]any {
	if output, ok := value.(map[string]any); ok {
		return output
	}
	return map[string]any{}
}
func sliceValue(value any) []any {
	if output, ok := value.([]any); ok {
		return output
	}
	return nil
}
func mapsFromSlice(value any) []map[string]any {
	values := sliceValue(value)
	output := make([]map[string]any, 0, len(values))
	for _, item := range values {
		output = append(output, mapValue(item))
	}
	return output
}
func stringValue(value any) string {
	if output, ok := value.(string); ok {
		return output
	}
	return ""
}
func stringOr(value any, fallback string) string {
	if output := stringValue(value); output != "" {
		return output
	}
	return fallback
}
func valueOr(value, fallback any) any {
	if value != nil {
		return value
	}
	return fallback
}
func boolValue(value any, fallback bool) bool {
	if output, ok := value.(bool); ok {
		return output
	}
	return fallback
}
func numberValue(value any) float64 {
	if output, ok := value.(float64); ok {
		return output
	}
	return 0
}
func first(values []any) any {
	if len(values) > 0 {
		return values[0]
	}
	return nil
}
func copyIfPresent(target, source map[string]any, names ...string) {
	for _, name := range names {
		if value, ok := source[name]; ok {
			target[name] = value
		}
	}
}
func nullableText(value any) any {
	if text := textFromContent(value); text != "" {
		return text
	}
	return nil
}
func textFromContent(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	var output strings.Builder
	for _, raw := range sliceValue(value) {
		block := mapValue(raw)
		if block["type"] == "text" || block["type"] == "output_text" {
			output.WriteString(stringValue(block["text"]))
		}
	}
	return output.String()
}
func toolResultText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if text := textFromContent(value); text != "" {
		return text
	}
	bytes, _ := json.Marshal(valueOr(value, ""))
	return string(bytes)
}

type classifiedToolBlocks struct {
	uses, results             []map[string]any
	clientUses, clientResults []map[string]any
	serverUses, serverResults []map[string]any
}

func classifyToolBlocks(blocks []map[string]any) classifiedToolBlocks {
	var output classifiedToolBlocks
	for _, block := range blocks {
		switch stringValue(block["type"]) {
		case "tool_use":
			output.uses = append(output.uses, block)
			output.clientUses = append(output.clientUses, block)
		case "tool_result":
			output.results = append(output.results, block)
			output.clientResults = append(output.clientResults, block)
		case "server_tool_use":
			output.uses = append(output.uses, block)
			output.serverUses = append(output.serverUses, block)
		case "web_search_tool_result", "web_fetch_tool_result":
			output.results = append(output.results, block)
			output.serverResults = append(output.serverResults, block)
		}
	}
	return output
}

func validateServerResults(uses, results []map[string]any) error {
	if len(results) == 0 {
		return nil
	}
	ids := make(map[string]struct{}, len(uses))
	for _, use := range uses {
		id := stringValue(use["id"])
		if id == "" {
			return errors.New("server_tool_use 缺少 id")
		}
		ids[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		id := resultID(result)
		if id == "" {
			return errors.New("server tool_result 缺少 tool_use_id")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("server tool_result id 重复：%s", id)
		}
		if _, exists := ids[id]; !exists {
			return fmt.Errorf("server tool_result 未匹配 server_tool_use：%s", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func toolBlocks(blocks []map[string]any) (uses, results []map[string]any) {
	classified := classifyToolBlocks(blocks)
	return classified.uses, classified.results
}
func upstreamToolName(name string, aliases map[string]string) string {
	if alias := aliases[name]; alias != "" {
		return alias
	}
	return name
}
func clientToolName(name string, aliases map[string]string) string {
	for original, alias := range aliases {
		if alias == name {
			return original
		}
	}
	return name
}
func isWebSearchTool(name string) bool {
	return nonLetters.ReplaceAllString(strings.ToLower(name), "") == "websearch"
}
func hasServerWebSearch(body map[string]any) bool {
	for _, raw := range sliceValue(body["tools"]) {
		tool := mapValue(raw)
		if strings.HasPrefix(stringValue(tool["type"]), "web_search_") && (tool["name"] == nil || isWebSearchTool(stringValue(tool["name"]))) {
			return true
		}
	}
	return false
}
func hasServerTool(tools []NormalizedToolCall) bool {
	for _, tool := range tools {
		if tool.ServerTool {
			return true
		}
	}
	return false
}
func usage(value any) (int, int) {
	object := mapValue(value)
	return int(numberValue(valueOr(object["prompt_tokens"], object["input_tokens"]))), int(numberValue(valueOr(object["completion_tokens"], object["output_tokens"])))
}
func stringsFrom(value any) []string {
	var output []string
	for _, item := range sliceValue(value) {
		if text := stringValue(item); text != "" {
			output = append(output, text)
		}
	}
	return output
}
func emptyToolSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}
func webSearchSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": "Web search query"}, "allowed_domains": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "blocked_domains": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "numResults": map[string]any{"type": "number"}}, "required": []any{"query"}, "additionalProperties": false}
}
func webFetchSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string"}, "format": map[string]any{"type": "string", "enum": []any{"text", "markdown", "html"}}, "timeout": map[string]any{"type": "number"}}, "required": []any{"url"}, "additionalProperties": false}
}

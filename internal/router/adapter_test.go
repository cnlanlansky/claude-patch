package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cnlanlansky/claude-patch/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func handlerClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Result(), nil
	})}
}

func TestProxyMessagesProtocolsHeadersAndFast(t *testing.T) {
	tests := []struct {
		name, protocol, path string
		body                 map[string]any
		response             string
		contentType          string
		check                func(*testing.T, *http.Request, map[string]any, *UpstreamResponse)
	}{
		{
			name: "anthropic", protocol: string(config.AnthropicMessages), path: "/api/v1/messages",
			body:     map[string]any{"model": "router-model", "messages": []any{}, "stream": false, "speed": "fast", "betas": []any{"fast-mode-2026-02-01", "other-beta"}},
			response: `{"ok":true}`, contentType: "application/json",
			check: func(t *testing.T, request *http.Request, body map[string]any, response *UpstreamResponse) {
				if request.Header.Get("X-Api-Key") != "provider-secret" || request.Header.Get("Authorization") != "" {
					t.Fatalf("Anthropic auth 错误：%v", request.Header)
				}
				if body["model"] != "upstream-model" || body["speed"] != nil || len(sliceValue(body["betas"])) != 1 {
					t.Fatalf("Anthropic body 错误：%v", body)
				}
			},
		},
		{
			name: "chat", protocol: string(config.OpenAIChat), path: "/api/v1/chat/completions",
			body:     map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}, "stream": false, "speed": "fast"},
			response: `{"id":"chat-1","choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`, contentType: "application/json",
			check: func(t *testing.T, _ *http.Request, body map[string]any, response *UpstreamResponse) {
				if body["model"] != "upstream-model" || body["service_tier"] != nil {
					t.Fatalf("Chat body 错误：%v", body)
				}
				var output map[string]any
				if json.Unmarshal(response.Body, &output) != nil || stringValue(mapValue(first(sliceValue(output["content"])))["text"]) != "hello" {
					t.Fatalf("Chat response 错误：%s", response.Body)
				}
			},
		},
		{
			name: "responses", protocol: string(config.OpenAIResponses), path: "/api/v1/responses",
			body:     map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}, "stream": false},
			response: `{"id":"response-1","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":2}}`, contentType: "application/json",
			check: func(t *testing.T, _ *http.Request, body map[string]any, response *UpstreamResponse) {
				if body["model"] != "upstream-model" || body["input"] == nil {
					t.Fatalf("Responses body 错误：%v", body)
				}
				var output map[string]any
				_ = json.Unmarshal(response.Body, &output)
				if stringValue(mapValue(first(sliceValue(output["content"])))["text"]) != "hello" {
					t.Fatalf("Responses response 错误：%s", response.Body)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var captured *http.Request
			var capturedBody map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				captured = request.Clone(request.Context())
				_ = json.NewDecoder(request.Body).Decode(&capturedBody)
				response.Header().Set("Content-Type", test.contentType)
				response.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(response, test.response)
			}))
			defer upstream.Close()
			provider := config.Provider{Label: "Demo", BaseURL: upstream.URL + "/api/v1", Protocol: config.Protocol(test.protocol), Auth: config.AuthAPIKey, APIKey: "provider-secret"}
			result, err := ProxyMessages(ProxyRequest{Model: "router-model", UpstreamModel: "upstream-model", Protocol: config.Protocol(test.protocol), Body: test.body, Headers: http.Header{"X-Api-Key": []string{"session-secret"}}}, provider, upstream.Client())
			if err != nil {
				t.Fatal(err)
			}
			if captured.URL.Path != test.path {
				t.Fatalf("上游路径错误：%s", captured.URL.Path)
			}
			test.check(t, captured, capturedBody, result)
		})
	}
}

func TestOpenCodeIdentityHeadersRequireOfficialHost(t *testing.T) {
	provider := config.Provider{Label: "Fake", BaseURL: "https://attacker.invalid/zen/v1", Protocol: config.OpenAIChat, Auth: config.AuthNone}
	headers := upstreamHeaders(provider, nil, false, config.OpenAIChat, "opencode-free", "session")
	if headers.Get("X-Opencode-Client") != "" {
		t.Fatalf("非官方 host 获得 OpenCode 身份头：%v", headers)
	}
}

func TestProxyMessagesOpenCodeCompatibilityAndStreaming(t *testing.T) {
	var captured http.Header
	var body map[string]any
	client := handlerClient(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		captured = request.Header.Clone()
		_ = json.NewDecoder(request.Body).Decode(&body)
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, strings.Join([]string{
			`data: {"id":"stream","choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`,
			"", `data: {"id":"stream","choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
			"", `data: {"id":"stream","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`,
			"", "data: [DONE]", "",
		}, "\n"))
	}))
	provider := config.Provider{Label: "OpenCode", BaseURL: "https://opencode.ai/zen/v1", Protocol: config.OpenAIChat, Auth: config.AuthNone}
	result, err := ProxyMessages(ProxyRequest{Model: "router-model", UpstreamModel: "upstream-model", ProviderID: "opencode-free", SessionID: "session-free", Protocol: config.OpenAIChat, ForceStreaming: true, Body: map[string]any{"messages": []any{}, "stream": false}, Headers: http.Header{"X-Opencode-Client": []string{"spoof"}}}, provider, client)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Get("X-Opencode-Client") != "cli" || captured.Get("X-Opencode-Session") != "ses_sessionfree" || !strings.HasPrefix(captured.Get("X-Opencode-Request"), "msg_") {
		t.Fatalf("OpenCode headers 错误：%v", captured)
	}
	if body["stream"] != true || mapValue(body["stream_options"])["include_usage"] != true {
		t.Fatalf("强制 streaming body 错误：%v", body)
	}
	var output map[string]any
	_ = json.Unmarshal(result.Body, &output)
	if stringValue(mapValue(first(sliceValue(output["content"])))["text"]) != "hello world" || mapValue(output["usage"])["input_tokens"] != float64(4) {
		t.Fatalf("stream 聚合错误：%s", result.Body)
	}
}

func TestProxyMessagesToolsAliasesAndErrors(t *testing.T) {
	var body map[string]any
	client := handlerClient(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = json.NewDecoder(request.Body).Decode(&body)
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, "event: response.output_item.added\n"+`data: {"item":{"type":"function_call","id":"fc-1","call_id":"call-1","name":"web_search_tool","arguments":"{\"query\":\"Guangzhou\"}"}}`+"\n\n")
	}))
	provider := config.Provider{Label: "OpenCode Go", BaseURL: "https://opencode.ai/zen/go/v1", Protocol: config.OpenAIResponses, Auth: config.AuthBearer, APIKey: "secret"}
	result, err := ProxyMessages(ProxyRequest{
		Model: "router-model", Protocol: config.OpenAIResponses, ForceStreaming: true,
		Body: map[string]any{
			"messages": []any{}, "stream": false,
			"tools": []any{map[string]any{"type": "web_search_20250305", "name": "web_search", "input_schema": nil}},
		},
		WebSearch: func(context.Context, string, SearchOptions) ([]SearchResult, error) {
			return []SearchResult{{Title: "广州天气", URL: "https://weather.invalid/guangzhou"}}, nil
		},
	}, provider, client)
	if err != nil {
		t.Fatal(err)
	}
	if stringValue(mapValue(first(sliceValue(body["tools"])))["name"]) != "web_search_tool" {
		t.Fatalf("Responses 工具别名错误：%v", body["tools"])
	}
	var output map[string]any
	_ = json.Unmarshal(result.Body, &output)
	content := sliceValue(output["content"])
	if len(content) != 2 || mapValue(content[0])["name"] != "web_search" || mapValue(content[1])["type"] != "web_search_tool_result" {
		t.Fatalf("server search response 错误：%s", result.Body)
	}
	_, err = ProxyMessages(ProxyRequest{Body: map[string]any{}}, config.Provider{Label: "Missing", BaseURL: "https://provider.invalid", Protocol: config.AnthropicMessages, Auth: config.AuthBearer}, client)
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("缺少 API key 未失败：%v", err)
	}
}

func TestOpenCodeGoAnthropicAuthenticationAndServerToolNormalization(t *testing.T) {
	var captured http.Header
	var body map[string]any
	client := handlerClient(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		captured = request.Header.Clone()
		_ = json.NewDecoder(request.Body).Decode(&body)
		response.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(response, `{}`)
	}))
	provider := config.Provider{Label: "OpenCode Go", BaseURL: "https://opencode.ai/zen/go/v1", Protocol: config.AnthropicMessages, Auth: config.AuthBearer, APIKey: "provider-secret"}
	result, err := ProxyMessages(ProxyRequest{
		Model: "router-model", UpstreamModel: "upstream-model", Protocol: config.AnthropicMessages,
		Body: map[string]any{
			"messages":    []any{map[string]any{"role": "user", "content": "search"}},
			"tools":       []any{map[string]any{"type": "web_search_20250305", "name": "web_search", "input_schema": nil}, map[string]any{"name": "regular", "input_schema": emptyToolSchema()}},
			"tool_choice": map[string]any{"type": "tool", "name": "web_search"}, "stream": false,
		},
		Headers: http.Header{"Authorization": []string{"Bearer session"}, "X-Api-Key": []string{"session"}},
	}, provider, client)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != http.StatusAccepted || captured.Get("X-Api-Key") != "provider-secret" || captured.Get("Authorization") != "" {
		t.Fatalf("OpenCode Go Anthropic 认证错误：%d %v", result.Status, captured)
	}
	tools := sliceValue(body["tools"])
	if len(tools) != 2 || mapValue(tools[0])["type"] != nil || stringValue(mapValue(tools[0])["name"]) != "web_search" || stringValue(mapValue(mapValue(tools[0])["input_schema"])["type"]) != "object" {
		t.Fatalf("OpenCode Go server tool 转换错误：%v", tools)
	}
	if stringValue(mapValue(body["tool_choice"])["type"]) != "auto" {
		t.Fatalf("OpenCode Go tool_choice 未归一化：%v", body["tool_choice"])
	}
}

func TestOpenAIToolSchemasDescriptionsAndConversationLinks(t *testing.T) {
	chat := toChatRequest(map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "search"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "call-search", "name": "web_search", "input": map[string]any{"query": "Guangzhou"}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "call-search", "content": []any{
					map[string]any{"type": "text", "text": "sunny"},
				}},
			}},
		},
		"tools": []any{
			map[string]any{"type": "web_search_20250305", "name": "web_search", "input_schema": nil},
			map[string]any{"type": "web_fetch_20250305", "name": "web_fetch", "input_schema": nil},
			map[string]any{"name": "custom"},
		},
	}, "upstream", false, false, nil)
	messages := sliceValue(chat["messages"])
	assistantCalls := sliceValue(mapValue(messages[1])["tool_calls"])
	if len(messages) != 3 || stringValue(mapValue(first(assistantCalls))["id"]) != "call-search" || stringValue(mapValue(messages[2])["tool_call_id"]) != "call-search" || mapValue(messages[2])["content"] != "sunny" {
		t.Fatalf("Chat tool 调用关系错误：%v", messages)
	}
	tools := sliceValue(chat["tools"])
	searchFunction := mapValue(mapValue(tools[0])["function"])
	fetchFunction := mapValue(mapValue(tools[1])["function"])
	customFunction := mapValue(mapValue(tools[2])["function"])
	fetchProperties := mapValue(mapValue(fetchFunction["parameters"])["properties"])
	fetchFormats := sliceValue(mapValue(fetchProperties["format"])["enum"])
	if stringValue(searchFunction["description"]) == "" || stringValue(mapValue(searchFunction["parameters"])["type"]) != "object" || stringValue(fetchFunction["description"]) == "" || first(fetchFormats) != "text" || stringValue(customFunction["description"]) == "" {
		t.Fatalf("OpenAI 工具 schema/description 错误：%v", tools)
	}

	responses := toResponsesRequest(map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "call-search", "name": "web_search", "input": map[string]any{"query": "Guangzhou"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "call-search", "content": "sunny"},
		}},
	}}, "upstream", false, false, nil)
	input := sliceValue(responses["input"])
	if len(input) != 2 || mapValue(input[0])["type"] != "function_call" || mapValue(input[1])["type"] != "function_call_output" || mapValue(input[1])["call_id"] != "call-search" || mapValue(input[1])["output"] != "sunny" {
		t.Fatalf("Responses tool result 关系错误：%v", input)
	}
}

func TestParseSearchHTMLIncludesSnippetsAndFiltersDomains(t *testing.T) {
	body := `<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fdocs.example.com%2Fguide">A &amp; B</a>` +
		`<a class="result__snippet">First &lt;snippet&gt;</a>` +
		`<a class="result__a" href="https://blocked.invalid/page">Blocked</a>` +
		`<a class="result__snippet">Second</a>`
	results := parseSearchHTML(body, SearchOptions{AllowedDomains: []string{"example.com"}})
	if len(results) != 1 || results[0].Title != "A & B" || results[0].Snippet != "First <snippet>" || results[0].URL != "https://docs.example.com/guide" {
		t.Fatalf("搜索 HTML 解析错误：%+v", results)
	}
}

func TestOpenAIChatStreamToolCallsMergeByNumericIndex(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"second","function":{"name":"second","arguments":"{\"value\":"}}, {"index":0,"id":"first","function":{"name":"first","arguments":"{\"value\":"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}, {"index":1,"function":{"arguments":"2}"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n"))
	value := normalizeOpenAIStream(raw, config.OpenAIChat, nil)
	if len(value.Tools) != 2 || value.Tools[0].ID != "second" || value.Tools[0].Name != "second" || value.Tools[0].Arguments != `{"value":2}` || value.Tools[1].ID != "first" || value.Tools[1].Name != "first" || value.Tools[1].Arguments != `{"value":1}` {
		t.Fatalf("数字 index 工具分片未正确合并：%+v", value.Tools)
	}
}

func TestOpenAIResponsesStreamToolCallsKeepItemKey(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`event: response.output_item.added`,
		`data: {"item":{"id":"item-1","call_id":"call-1","name":"lookup","arguments":""}}`,
		"",
		`event: response.function_call_arguments.delta`,
		`data: {"item_id":"item-1","delta":"{\"query\":"}`,
		"",
		`event: response.function_call_arguments.delta`,
		`data: {"item_id":"item-1","delta":"\"weather\"}"}`,
		"",
		`event: response.completed`,
		`data: {"response":{"id":"response-1"}}`,
		"",
	}, "\n"))
	value := normalizeOpenAIStream(raw, config.OpenAIResponses, nil)
	if len(value.Tools) != 1 || value.Tools[0].ID != "call-1" || value.Tools[0].Name != "lookup" || value.Tools[0].Arguments != `{"query":"weather"}` || value.FinishReason != "stop" {
		t.Fatalf("Responses 工具分片错误：%+v finish=%q", value.Tools, value.FinishReason)
	}
}
func TestStreamToolOrderIsStable(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"second","function":{"name":"second","arguments":"{}"}},{"index":0,"id":"first","function":{"name":"first","arguments":"{}"}}]}}]}`,
		"", "data: [DONE]", "",
	}, "\n"))
	for iteration := 0; iteration < 20; iteration++ {
		value := normalizeOpenAIStream(raw, config.OpenAIChat, nil)
		if len(value.Tools) != 2 || value.Tools[0].ID != "second" || value.Tools[1].ID != "first" {
			t.Fatalf("工具顺序不稳定：%+v", value.Tools)
		}
	}
}

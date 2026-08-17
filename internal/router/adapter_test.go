package router

import (
	"context"
	"encoding/json"
	"errors"
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
	headers := providerAdapterFor("opencode-free").headers(provider, nil, false, config.OpenAIChat, "opencode-free", "session")
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
		Model: "router-model", ProviderID: "opencode-go", Protocol: config.OpenAIResponses, ForceStreaming: true,
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
	searchContent := sliceValue(mapValue(content[1])["content"])
	if len(searchContent) != 1 || mapValue(searchContent[0])["type"] != "web_search_result" || mapValue(searchContent[0])["title"] != "广州天气" {
		t.Fatalf("搜索结果 block schema 错误：%s", result.Body)
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
		Model: "router-model", UpstreamModel: "upstream-model", ProviderID: "opencode-go", Protocol: config.AnthropicMessages,
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
	chat, err := toChatRequest(map[string]any{
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
	if err != nil {
		t.Fatal(err)
	}
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

	responses, err := toResponsesRequest(map[string]any{"messages": []any{
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

func TestProviderAdaptersKeepUpstreamPoliciesIsolated(t *testing.T) {
	capture := func(t *testing.T, request ProxyRequest, provider config.Provider) (http.Header, map[string]any) {
		t.Helper()
		var captured http.Header
		var body map[string]any
		client := handlerClient(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			captured = request.Header.Clone()
			_ = json.NewDecoder(request.Body).Decode(&body)
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"response-1","output":[]}`)
		}))
		if _, err := ProxyMessages(request, provider, client); err != nil {
			t.Fatal(err)
		}
		return captured, body
	}

	subHeaders, _ := capture(t, ProxyRequest{
		Model: "router-model", ProviderID: "sub2api", Protocol: config.AnthropicMessages,
		Body: map[string]any{"messages": []any{}, "stream": false},
	}, config.Provider{Label: "Sub2API", BaseURL: "https://opencode.ai/zen/go/v1", Protocol: config.AnthropicMessages, Auth: config.AuthBearer, APIKey: "sub-secret"})
	if subHeaders.Get("X-Opencode-Client") != "" || subHeaders.Get("X-Api-Key") != "" || subHeaders.Get("Authorization") != "Bearer sub-secret" {
		t.Fatalf("Sub2API 泄漏 OpenCode 认证策略：%v", subHeaders)
	}

	freeHeaders, freeBody := capture(t, ProxyRequest{
		Model: "router-model", ProviderID: "opencode-free", Protocol: config.OpenAIChat,
		SessionID: "session-free", Body: map[string]any{
			"messages": []any{}, "stream": false,
			"tools": []any{map[string]any{"type": "web_search_20250305", "name": "web_search"}},
		},
	}, config.Provider{Label: "OpenCode Free", BaseURL: "https://opencode.ai/zen/v1", Protocol: config.OpenAIChat, Auth: config.AuthNone})
	freeTools := sliceValue(freeBody["tools"])
	freeFunction := mapValue(mapValue(first(freeTools))["function"])
	if freeHeaders.Get("X-Opencode-Client") != "cli" || freeHeaders.Get("X-Api-Key") != "" || stringValue(freeFunction["name"]) != "web_search" {
		t.Fatalf("OpenCode Free 策略错误或混入 Go alias：%v %v", freeHeaders, freeBody)
	}

	goHeaders, goBody := capture(t, ProxyRequest{
		Model: "router-model", ProviderID: "opencode-go", Protocol: config.OpenAIResponses,
		Body: map[string]any{
			"messages": []any{}, "stream": false,
			"tools": []any{map[string]any{"type": "web_search_20250305", "name": "web_search"}},
		},
	}, config.Provider{Label: "OpenCode Go", BaseURL: "https://opencode.ai/zen/go/v1", Protocol: config.OpenAIResponses, Auth: config.AuthBearer, APIKey: "go-secret"})
	goTools := sliceValue(goBody["tools"])
	goFunction := mapValue(first(goTools))
	if goHeaders.Get("X-Opencode-Client") != "" || goHeaders.Get("Authorization") != "Bearer go-secret" || stringValue(goFunction["name"]) != "web_search_tool" || goBody["stream"] != true {
		t.Fatalf("OpenCode Go 策略错误：%v %v", goHeaders, goBody)
	}
}

func TestServerSearchEmptyResultsAreArraysAcrossProtocols(t *testing.T) {
	tests := []struct {
		name, protocol, response string
	}{
		{name: "sub2api-anthropic", protocol: string(config.AnthropicMessages), response: `{"id":"message-1","content":[{"type":"tool_use","id":"search-1","name":"web_search","input":{"query":"OpenAI"}}]}`},
		{name: "opencode-free-chat", protocol: string(config.OpenAIChat), response: `{"id":"chat-1","choices":[{"message":{"tool_calls":[{"id":"search-1","function":{"name":"web_search","arguments":"{\"query\":\"OpenAI\"}"}}]},"finish_reason":"tool_calls"}]}`},
		{name: "opencode-go-responses", protocol: string(config.OpenAIResponses), response: `{"id":"response-1","output":[{"type":"function_call","call_id":"search-1","name":"web_search","arguments":"{\"query\":\"OpenAI\"}"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := handlerClient(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(response, test.response)
			}))
			providerID := "sub2api"
			if test.name == "opencode-free-chat" {
				providerID = "opencode-free"
			} else if test.name == "opencode-go-responses" {
				providerID = "opencode-go"
			}
			result, err := ProxyMessages(ProxyRequest{
				Model: "router-model", ProviderID: providerID, Protocol: config.Protocol(test.protocol),
				Body: map[string]any{"messages": []any{}, "stream": false, "tools": []any{map[string]any{"type": "web_search_20250305", "name": "web_search"}}},
				WebSearch: func(context.Context, string, SearchOptions) ([]SearchResult, error) {
					return nil, nil
				},
			}, config.Provider{Label: test.name, BaseURL: "https://provider.invalid", Protocol: config.Protocol(test.protocol), Auth: config.AuthNone}, client)
			if err != nil {
				t.Fatal(err)
			}
			var output map[string]any
			if err := json.Unmarshal(result.Body, &output); err != nil {
				t.Fatal(err)
			}
			content := sliceValue(output["content"])
			if len(content) != 2 {
				t.Fatalf("搜索响应 block 数量错误：%s", result.Body)
			}
			searchContent, ok := mapValue(content[1])["content"]
			if !ok || searchContent == nil || sliceValue(searchContent) == nil || len(sliceValue(searchContent)) != 0 {
				t.Fatalf("无结果搜索必须返回空数组：%s", result.Body)
			}
		})
	}
}

func TestSearchErrorCodePreservesTypedCode(t *testing.T) {
	value := resolveSearches(context.Background(), NormalizedResponse{Tools: []NormalizedToolCall{{ID: "search-1", Name: "web_search", ServerTool: true, Arguments: `{"query":"OpenAI"}`}}}, func(context.Context, string, SearchOptions) ([]SearchResult, error) {
		return nil, &SearchError{Code: "invalid_tool_input", Err: errors.New("bad query")}
	})
	if len(value) != 1 || value[0].ErrorCode != "invalid_tool_input" {
		t.Fatalf("搜索错误码未保留：%+v", value)
	}
}

func TestSearchResultBlocksPreserveAnthropicFields(t *testing.T) {
	blocks := searchResultBlocks([]SearchResult{{Title: "OpenAI", URL: "https://openai.com/", Snippet: "official", EncryptedContent: "opaque", PageAge: "today"}})
	if len(blocks) != 1 {
		t.Fatalf("搜索结果 block 数量错误：%v", blocks)
	}
	block := mapValue(blocks[0])
	if block["type"] != "web_search_result" || block["encrypted_content"] != "opaque" || block["page_age"] != "today" {
		t.Fatalf("搜索结果字段丢失：%v", block)
	}
}

func TestSearchErrorUsesAnthropicErrorShape(t *testing.T) {
	client := handlerClient(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"id":"chat-1","choices":[{"message":{"tool_calls":[{"id":"search-1","function":{"name":"web_search","arguments":"{\"query\":\"OpenAI\"}"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	result, err := ProxyMessages(ProxyRequest{
		Model: "router-model", Protocol: config.OpenAIChat,
		Body: map[string]any{"messages": []any{}, "stream": false, "tools": []any{map[string]any{"type": "web_search_20250305", "name": "web_search"}}},
		WebSearch: func(context.Context, string, SearchOptions) ([]SearchResult, error) {
			return nil, errors.New("search upstream returned HTTP 503")
		},
	}, config.Provider{Label: "Fake", BaseURL: "https://provider.invalid", Protocol: config.OpenAIChat, Auth: config.AuthNone}, client)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(result.Body, &output); err != nil {
		t.Fatal(err)
	}
	content := sliceValue(output["content"])
	if len(content) != 2 {
		t.Fatalf("搜索错误响应 block 数量错误：%s", result.Body)
	}
	errorContent := mapValue(mapValue(content[1])["content"])
	if errorContent["type"] != "web_search_tool_result_error" || errorContent["error_code"] != "unavailable" {
		t.Fatalf("搜索错误 schema 错误：%s", result.Body)
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

func TestChatMixedToolResultContentKeepsAssistantToolUserOrder(t *testing.T) {
	chat, err := toChatRequest(map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "call-1", "name": "lookup", "input": map[string]any{"query": "weather"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "继续"},
			map[string]any{"type": "tool_result", "tool_use_id": "call-1", "content": "晴天"},
		}},
	}}, "upstream", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	messages := sliceValue(chat["messages"])
	if len(messages) != 3 || stringValue(mapValue(messages[0])["role"]) != "assistant" || stringValue(mapValue(messages[1])["role"]) != "tool" || stringValue(mapValue(messages[2])["role"]) != "user" {
		t.Fatalf("混合 tool_result 顺序错误：%v", messages)
	}
	call := mapValue(first(sliceValue(mapValue(messages[0])["tool_calls"])))
	if stringValue(call["id"]) != "call-1" || stringValue(mapValue(messages[1])["tool_call_id"]) != "call-1" || mapValue(messages[1])["content"] != "晴天" || mapValue(messages[2])["content"] != "继续" {
		t.Fatalf("混合 tool_result 内容错误：%v", messages)
	}
}

func TestChatServerToolResultHistoryIsAccepted(t *testing.T) {
	chat, err := toChatRequest(map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "查询中"},
			map[string]any{"type": "server_tool_use", "id": "server-1", "name": "web_search", "input": map[string]any{"query": "weather"}},
			map[string]any{"type": "web_search_tool_result", "tool_use_id": "server-1", "content": "晴天"},
		}},
		map[string]any{"role": "user", "content": "继续"},
	}}, "upstream", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	messages := sliceValue(chat["messages"])
	if len(messages) != 3 || stringValue(mapValue(messages[0])["role"]) != "assistant" || stringValue(mapValue(messages[1])["role"]) != "tool" || stringValue(mapValue(messages[1])["tool_call_id"]) != "server-1" || stringValue(mapValue(messages[2])["role"]) != "user" {
		t.Fatalf("server tool 历史转换错误：%v", messages)
	}
}

func TestAnthropicServerToolResultHistoryIsAccepted(t *testing.T) {
	client := handlerClient(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(response, `{}`)
	}))
	provider := config.Provider{Label: "OpenCode Go", BaseURL: "https://opencode.ai/zen/go/v1", Protocol: config.AnthropicMessages, Auth: config.AuthBearer, APIKey: "test-key"}
	_, err := ProxyMessages(ProxyRequest{Model: "router-model", Protocol: config.AnthropicMessages, Body: map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "server_tool_use", "id": "server-1", "name": "web_search", "input": map[string]any{"query": "weather"}},
			map[string]any{"type": "web_search_tool_result", "tool_use_id": "server-1", "content": "晴天"},
		}},
	}}}, provider, client)
	if err != nil {
		t.Fatal(err)
	}
}
func TestChatToolIDsFailBeforeProxyRequest(t *testing.T) {
	tests := []struct {
		name, want string
		messages   []any
	}{
		{
			name: "missing", want: "缺少 id",
			messages: []any{map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "name": "lookup", "input": map[string]any{}},
			}}},
		},
		{
			name: "unknown", want: "未匹配",
			messages: []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "missing", "content": "结果"},
			}}},
		},
		{
			name: "duplicate", want: "重复",
			messages: []any{map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "call-1", "name": "first", "input": map[string]any{}},
				map[string]any{"type": "tool_use", "id": "call-1", "name": "second", "input": map[string]any{}},
			}}},
		},
		{
			name: "unclosed", want: "未收到",
			messages: []any{map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "call-open", "name": "lookup", "input": map[string]any{}},
			}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			client := handlerClient(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = io.WriteString(response, `{}`)
			}))
			_, err := ProxyMessages(ProxyRequest{
				Model: "router-model", Protocol: config.OpenAIChat,
				Body: map[string]any{"messages": test.messages, "stream": false},
			}, config.Provider{Label: "Fake", BaseURL: "https://provider.invalid", Protocol: config.OpenAIChat, Auth: config.AuthNone}, client)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("tool ID 错误未拒绝：%v", err)
			}
			if requests != 0 {
				t.Fatalf("转换失败后仍发送上游请求：%d", requests)
			}
		})
	}
}

func TestResponsesMixedToolOrderingAndMissingID(t *testing.T) {
	responses, err := toResponsesRequest(map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "先查"},
			map[string]any{"type": "tool_use", "id": "call-1", "name": "lookup", "input": map[string]any{"query": "weather"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "继续"},
			map[string]any{"type": "tool_result", "tool_use_id": "call-1", "content": "晴天"},
		}},
	}}, "upstream", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := sliceValue(responses["input"])
	if len(input) != 4 || stringValue(mapValue(input[0])["role"]) != "assistant" || mapValue(input[1])["type"] != "function_call" || mapValue(input[2])["type"] != "function_call_output" || stringValue(mapValue(input[3])["role"]) != "user" {
		t.Fatalf("Responses 混合顺序错误：%v", input)
	}
	if mapValue(input[0])["content"] != "先查" || stringValue(mapValue(input[1])["call_id"]) != "call-1" || mapValue(input[2])["output"] != "晴天" || mapValue(input[3])["content"] != "继续" {
		t.Fatalf("Responses 混合内容错误：%v", input)
	}
	_, err = toResponsesRequest(map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "name": "lookup", "input": map[string]any{}},
	}}}}, "upstream", false, false, nil)
	if err == nil || !strings.Contains(err.Error(), "缺少 id") {
		t.Fatalf("Responses 缺失 tool_use ID 未拒绝：%v", err)
	}
}

func TestOpenCodeFreeReasoningRequestAndResponseRoundTrip(t *testing.T) {
	body := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "先分析"},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "思考内容"},
			map[string]any{"type": "text", "text": "答案"},
		}},
	}, "stream": false}
	request, err := toChatRequestWithReasoning(body, "deepseek-v4-flash-free", false, false, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	messages := sliceValue(request["messages"])
	assistant := mapValue(messages[1])
	if assistant["reasoning_content"] != "思考内容" || assistant["content"] != "答案" {
		t.Fatalf("thinking 未转换为 reasoning_content：%v", assistant)
	}

	normalized := normalizeChatWithReasoning(map[string]any{
		"id": "chat-1", "choices": []any{map[string]any{"message": map[string]any{
			"reasoning_content": "思考内容", "content": "答案",
		}, "finish_reason": "stop"}},
	}, true)
	response := anthropicResponse(normalized, "router-model", nil, true)
	content := sliceValue(response["content"])
	if len(content) != 2 || mapValue(content[0])["type"] != "thinking" || mapValue(content[0])["thinking"] != "思考内容" || mapValue(content[1])["text"] != "答案" {
		t.Fatalf("thinking 响应 schema 错误：%v", content)
	}
}

func TestOpenCodeFreeReasoningToolContinuation(t *testing.T) {
	body := map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "先查天气"},
			map[string]any{"type": "text", "text": "我来查询"},
			map[string]any{"type": "tool_use", "id": "call-1", "name": "lookup", "input": map[string]any{"query": "天气"}},
		}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call-1", "content": "晴天"}}},
	}, "stream": false}
	request, err := toChatRequestWithReasoning(body, "deepseek-v4-flash-free", false, false, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	messages := sliceValue(request["messages"])
	assistant := mapValue(messages[0])
	if assistant["reasoning_content"] != "先查天气" || len(sliceValue(assistant["tool_calls"])) != 1 || stringValue(mapValue(messages[1])["tool_call_id"]) != "call-1" {
		t.Fatalf("thinking/tool continuation 错误：%v", messages)
	}
}

func TestOpenCodeFreeReasoningStream(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`data: {"id":"chat-1","choices":[{"delta":{"reasoning_content":"思考"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chat-1","choices":[{"delta":{"reasoning_content":"中"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chat-1","choices":[{"delta":{"content":"答案"},"finish_reason":"stop"}]}`,
		"", "data: [DONE]", "",
	}, "\n"))
	normalized := normalizeOpenAIStreamWithReasoning(raw, config.OpenAIChat, nil, true)
	if normalized.ReasoningContent != "思考中" || normalized.Text != "答案" {
		t.Fatalf("SSE reasoning 聚合错误：%+v", normalized)
	}
	stream := anthropicStream(normalized, "router-model", nil, true)
	if !strings.Contains(string(stream), `"type":"thinking_delta"`) || !strings.Contains(string(stream), `"thinking":"思考中"`) {
		t.Fatalf("Anthropic thinking SSE 错误：%s", stream)
	}
}

func TestNonOpenCodeChatReasoningRemainsDisabled(t *testing.T) {
	body := map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "不要注入"}}}}}
	request, err := toChatRequest(body, "upstream", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	assistant := mapValue(first(sliceValue(request["messages"])))
	if _, exists := assistant["reasoning_content"]; exists {
		t.Fatalf("通用 Chat 意外注入 reasoning_content：%v", assistant)
	}
}

func TestOpenCodeFreeProxyReasoningRoundTrip(t *testing.T) {
	var requests []map[string]any
	client := handlerClient(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, body)
		response.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			_, _ = io.WriteString(response, `{"id":"chat-1","choices":[{"message":{"reasoning_content":"思考内容","content":"答案"},"finish_reason":"stop"}]}`)
			return
		}
		_, _ = io.WriteString(response, `{"id":"chat-2","choices":[{"message":{"content":"继续答案"},"finish_reason":"stop"}]}`)
	}))
	provider := config.Provider{Label: "OpenCode Free", BaseURL: "https://provider.invalid/zen/v1", Protocol: config.OpenAIChat, Auth: config.AuthNone}
	first, err := ProxyMessages(ProxyRequest{
		Model: "router-model", UpstreamModel: "deepseek-v4-flash-free", ProviderID: "opencode-free", Protocol: config.OpenAIChat,
		Body: map[string]any{"messages": []any{map[string]any{"role": "user", "content": "开始"}}, "stream": false},
	}, provider, client)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(first.Body, &output); err != nil {
		t.Fatal(err)
	}
	content := sliceValue(output["content"])
	if len(content) != 2 || mapValue(content[0])["type"] != "thinking" {
		t.Fatalf("Router 未返回 thinking block：%s", first.Body)
	}
	secondBody := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "开始"},
		map[string]any{"role": "assistant", "content": content},
		map[string]any{"role": "user", "content": "继续"},
	}, "stream": false}
	if _, err := ProxyMessages(ProxyRequest{
		Model: "router-model", UpstreamModel: "deepseek-v4-flash-free", ProviderID: "opencode-free", Protocol: config.OpenAIChat,
		Body: secondBody,
	}, provider, client); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("fake upstream 请求次数错误：%d", len(requests))
	}
	messages := sliceValue(requests[1]["messages"])
	assistant := mapValue(messages[1])
	if assistant["reasoning_content"] != "思考内容" {
		t.Fatalf("第二轮未回传 reasoning_content：%v", assistant)
	}
}

func TestProviderReasoningPolicyIsolated(t *testing.T) {
	body := map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "内部推理"}}}}}
	for _, providerID := range []string{"sub2api", "opencode-go", "custom"} {
		prepared, err := providerAdapterFor(providerID).prepare(ProxyRequest{ProviderID: providerID, Protocol: config.OpenAIChat, Body: body}, config.Provider{Protocol: config.OpenAIChat}, "model", false)
		if err != nil {
			t.Fatalf("%s prepare 失败：%v", providerID, err)
		}
		if prepared.PreserveReasoningContent {
			t.Fatalf("%s 意外启用 reasoning policy", providerID)
		}
		assistant := mapValue(first(sliceValue(prepared.Payload["messages"])))
		if _, exists := assistant["reasoning_content"]; exists {
			t.Fatalf("%s 意外注入 reasoning_content：%v", providerID, assistant)
		}
	}
}

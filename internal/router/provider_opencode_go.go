package router

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/cnlanlansky/claude-patch/internal/config"
)

type openCodeGoAdapter struct{}

func (openCodeGoAdapter) prepare(request ProxyRequest, provider config.Provider, model string, fast bool) (preparedUpstream, error) {
	aliases := responseToolAliases(provider, request.Protocol, request.Body)
	if request.Protocol == config.OpenAIChat {
		payload, err := toChatRequestWithReasoning(request.Body, model, fast, true, aliases, true)
		if err != nil {
			return preparedUpstream{}, err
		}
		return preparedUpstream{Payload: payload, Path: "/v1/chat/completions", Aliases: aliases, PreserveReasoningContent: true}, nil
	}
	normalizeTools := request.Protocol == config.AnthropicMessages && isOpenCodeGo(provider.BaseURL)
	payload, path, err := prepareProtocolRequest(request.Body, request.Protocol, model, fast, true, normalizeTools, aliases)
	if err != nil {
		return preparedUpstream{}, err
	}
	return preparedUpstream{Payload: payload, Path: path, Aliases: aliases}, nil
}

func (openCodeGoAdapter) headers(provider config.Provider, incoming http.Header, fast bool, protocol config.Protocol, _, _ string) http.Header {
	headers := genericUpstreamHeaders(provider, incoming, fast)
	if protocol == config.AnthropicMessages && isOpenCodeGo(provider.BaseURL) {
		headers.Del("Authorization")
		headers.Set("X-Api-Key", provider.APIKey)
	}
	return headers
}

func isOpenCodeGo(base string) bool {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	return err == nil && strings.EqualFold(parsed.Hostname(), "opencode.ai") && parsed.Path == "/zen/go/v1"
}

func responseToolAliases(provider config.Provider, protocol config.Protocol, body map[string]any) map[string]string {
	aliases := map[string]string{}
	if protocol != config.OpenAIResponses || !isOpenCodeGo(provider.BaseURL) {
		return aliases
	}
	names := map[string]bool{}
	for _, raw := range sliceValue(body["tools"]) {
		names[stringValue(mapValue(raw)["name"])] = true
	}
	for _, raw := range sliceValue(body["tools"]) {
		tool := mapValue(raw)
		if stringValue(tool["name"]) != "web_search" || !strings.HasPrefix(stringValue(tool["type"]), "web_search_") {
			continue
		}
		alias := "web_search_tool"
		for names[alias] {
			alias = "claude_" + alias
		}
		aliases["web_search"] = alias
		names[alias] = true
	}
	return aliases
}

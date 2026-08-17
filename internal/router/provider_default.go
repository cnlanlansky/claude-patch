package router

import (
	"net/http"
	"strings"

	"github.com/cnlanlansky/claude-patch/internal/config"
)

type defaultProviderAdapter struct{}

func (defaultProviderAdapter) prepare(request ProxyRequest, _ config.Provider, model string, fast bool) (preparedUpstream, error) {
	payload, path, err := prepareProtocolRequest(request.Body, request.Protocol, model, fast, request.ForceStreaming, false, nil)
	if err != nil {
		return preparedUpstream{}, err
	}
	return preparedUpstream{Payload: payload, Path: path}, nil
}

func (defaultProviderAdapter) headers(provider config.Provider, incoming http.Header, fast bool, _ config.Protocol, _, _ string) http.Header {
	return genericUpstreamHeaders(provider, incoming, fast)
}

func prepareProtocolRequest(body map[string]any, protocol config.Protocol, model string, allowFast, forceStreaming, normalizeAnthropicTools bool, aliases map[string]string) (map[string]any, string, error) {
	var (
		payload map[string]any
		path    = "/v1/messages"
		err     error
	)
	switch protocol {
	case config.OpenAIChat:
		payload, err = toChatRequest(body, model, allowFast, forceStreaming, aliases)
		path = "/v1/chat/completions"
	case config.OpenAIResponses:
		payload, err = toResponsesRequest(body, model, allowFast, forceStreaming, aliases)
		path = "/v1/responses"
	default:
		payload, err = anthropicBody(body, model, allowFast, normalizeAnthropicTools)
	}
	return payload, path, err
}

func genericUpstreamHeaders(provider config.Provider, incoming http.Header, fast bool) http.Header {
	headers := incoming.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	for _, name := range []string{"Host", "Content-Length", "X-Api-Key", "Authorization"} {
		headers.Del(name)
	}
	if !fast {
		values := splitHeader(headers.Get("Anthropic-Beta"))
		filtered := values[:0]
		for _, value := range values {
			if value != "fast-mode-2026-02-01" {
				filtered = append(filtered, value)
			}
		}
		if len(filtered) > 0 {
			headers.Set("Anthropic-Beta", strings.Join(filtered, ","))
		} else {
			headers.Del("Anthropic-Beta")
		}
	}
	if provider.Auth == config.AuthAPIKey {
		headers.Set("X-Api-Key", provider.APIKey)
	} else if provider.Auth == config.AuthBearer {
		headers.Set("Authorization", "Bearer "+provider.APIKey)
	}
	headers.Set("Content-Type", "application/json")
	return headers
}

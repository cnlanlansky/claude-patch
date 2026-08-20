package router

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/cnlanlansky/claude-patch/internal/config"
)

type openCodeFreeAdapter struct{}

func (openCodeFreeAdapter) prepare(request ProxyRequest, _ config.Provider, model string, fast bool) (preparedUpstream, error) {
	if request.Protocol == config.OpenAIChat {
		// OpenCode CLI 的 Chat 协议始终使用 streaming，并要求上游附带 usage。
		payload, err := toChatRequestWithReasoning(request.Body, model, fast, true, nil, true)
		if err != nil {
			return preparedUpstream{}, err
		}
		if model == "big-pickle" && stringValue(payload["reasoning_effort"]) == "max" {
			// Big Pickle 未声明 max variant，Zen 收到该值会返回 Endpoint is unavailable。
			delete(payload, "reasoning_effort")
		}
		return preparedUpstream{Payload: payload, Path: "/v1/chat/completions", PreserveReasoningContent: true}, nil
	}
	payload, path, err := prepareProtocolRequest(request.Body, request.Protocol, model, fast, request.ForceStreaming, false, nil)
	if err != nil {
		return preparedUpstream{}, err
	}
	return preparedUpstream{Payload: payload, Path: path}, nil
}

func (openCodeFreeAdapter) headers(provider config.Provider, incoming http.Header, fast bool, protocol config.Protocol, _, sessionID string) http.Header {
	headers := genericUpstreamHeaders(provider, incoming, fast)
	if protocol != config.OpenAIChat || !isOpenCodeFree(provider.BaseURL) {
		return headers
	}
	for _, name := range []string{"X-Opencode-Client", "X-Opencode-Project", "X-Opencode-Request", "X-Opencode-Session", "X-Session-Affinity", "X-Session-Id"} {
		headers.Del(name)
	}
	headers.Set("User-Agent", "opencode/1.18.18 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14")
	headers.Set("X-Opencode-Client", "cli")
	headers.Set("X-Opencode-Project", "global")
	headers.Set("X-Opencode-Request", "msg_"+randomHex(16))
	clean := strings.ReplaceAll(sessionID, "-", "")
	if clean == "" {
		clean = randomHex(16)
	}
	if !strings.HasPrefix(clean, "ses_") {
		clean = "ses_" + clean
	}
	headers.Set("X-Opencode-Session", clean)
	return headers
}

func isOpenCodeFree(base string) bool {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	return err == nil && strings.EqualFold(parsed.Hostname(), "opencode.ai") && parsed.Path == "/zen/v1"
}

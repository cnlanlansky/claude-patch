package router

import (
	"net/http"

	"github.com/cnlanlansky/claude-patch/internal/config"
)

type preparedUpstream struct {
	Payload map[string]any
	Path    string
	Aliases map[string]string
}

type upstreamAdapter interface {
	prepare(ProxyRequest, config.Provider, string, bool) (preparedUpstream, error)
	headers(config.Provider, http.Header, bool, config.Protocol, string, string) http.Header
}

func providerAdapterFor(id string) upstreamAdapter {
	switch id {
	case "sub2api":
		return sub2APIAdapter{}
	case "opencode-free":
		return openCodeFreeAdapter{}
	case "opencode-go":
		return openCodeGoAdapter{}
	default:
		return defaultProviderAdapter{}
	}
}

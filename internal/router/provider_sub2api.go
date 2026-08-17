package router

import (
	"net/http"

	"github.com/cnlanlansky/claude-patch/internal/config"
)

type sub2APIAdapter struct{}

func (sub2APIAdapter) prepare(request ProxyRequest, _ config.Provider, model string, fast bool) (preparedUpstream, error) {
	payload, path, err := prepareProtocolRequest(request.Body, request.Protocol, model, fast, request.ForceStreaming, false, nil)
	if err != nil {
		return preparedUpstream{}, err
	}
	return preparedUpstream{Payload: payload, Path: path}, nil
}

func (sub2APIAdapter) headers(provider config.Provider, incoming http.Header, fast bool, _ config.Protocol, _, _ string) http.Header {
	return genericUpstreamHeaders(provider, incoming, fast)
}

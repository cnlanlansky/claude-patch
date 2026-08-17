package router

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cnlanlansky/claude-patch/internal/claude"
	"github.com/cnlanlansky/claude-patch/internal/config"
	webui "github.com/cnlanlansky/claude-patch/internal/web"
)

const maxBodyBytes = 16 * 1024 * 1024

type Session struct {
	ID        string          `json:"id"`
	Token     string          `json:"-"`
	ProcessID uint32          `json:"processId"`
	StartedAt string          `json:"startedAt"`
	Child     *claude.Session `json:"-"`
}

type Options struct {
	ConfigPath      string
	ExecutablePath  string
	CommandPath     string
	InitialConfig   config.Config
	Client          *http.Client
	Registry        *Registry
	DiscoverClaude  func(string) (claude.Discovery, error)
	OnConfigChanged func(config.Config)
	OnStopClaude    func(string) error
}

type Server struct {
	Origin string

	options  Options
	listener net.Listener
	http     *http.Server
	registry *Registry

	mu       sync.RWMutex
	value    config.Config
	sessions map[string]Session
	stopping bool
	closed   chan struct{}
	stopOnce sync.Once
	stopErr  error
}

func Start(options Options) (*Server, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	registry := options.Registry
	if registry == nil {
		registry = NewRegistry()
	}
	server := &Server{
		Origin:  "http://" + listener.Addr().String(),
		options: options, listener: listener, registry: registry,
		value: options.InitialConfig, sessions: make(map[string]Session), closed: make(chan struct{}),
	}
	server.http = &http.Server{Handler: server, ReadHeaderTimeout: 15 * time.Second}
	go func() {
		_ = server.http.Serve(listener)
		close(server.closed)
	}()
	return server, nil
}

func (server *Server) ManagementURL() string { return server.Origin + "/" }

func (server *Server) Closed() <-chan struct{} { return server.closed }

func (server *Server) Config() config.Config {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return cloneConfig(server.value)
}

func (server *Server) Register(session Session) error {
	if session.ID == "" || session.Token == "" || session.ProcessID == 0 {
		return errors.New("Claude session 元数据无效")
	}
	server.mu.Lock()
	if server.stopping {
		server.mu.Unlock()
		return errors.New("Router 正在停止")
	}
	if _, exists := server.sessions[session.ID]; exists {
		server.mu.Unlock()
		return fmt.Errorf("Claude session 已存在：%s", session.ID)
	}
	server.sessions[session.ID] = session
	server.mu.Unlock()
	if err := server.registry.Register(RegistryRecord{ID: session.ID, ProcessID: session.ProcessID, StartedAt: session.StartedAt}); err != nil {
		server.mu.Lock()
		delete(server.sessions, session.ID)
		server.mu.Unlock()
		return err
	}
	return nil
}

func (server *Server) Remove(id string) {
	server.mu.Lock()
	delete(server.sessions, id)
	server.mu.Unlock()
	_ = server.registry.Remove(id)
}

func (server *Server) Stop() error {
	server.stopOnce.Do(func() {
		server.mu.Lock()
		server.stopping = true
		sessions := make([]Session, 0, len(server.sessions))
		for _, session := range server.sessions {
			sessions = append(sessions, session)
		}
		server.sessions = make(map[string]Session)
		server.mu.Unlock()
		var errs []error
		for _, session := range sessions {
			if server.options.OnStopClaude != nil {
				errs = append(errs, server.options.OnStopClaude(session.ID))
			} else if session.Child != nil {
				errs = append(errs, session.Child.Stop())
			}
			errs = append(errs, server.registry.Remove(session.ID))
		}
		errs = append(errs, server.registry.Close())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		errs = append(errs, server.http.Shutdown(ctx))
		server.stopErr = errors.Join(errs...)
	})
	return server.stopErr
}

func (server *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !loopbackHost(request.Host) {
		jsonResponse(response, http.StatusForbidden, map[string]any{"error": "invalid_host"})
		return
	}
	path := request.URL.Path
	if request.Method == http.MethodGet && path == "/" {
		server.serveManagement(response, request)
		return
	}
	if request.Method == http.MethodGet && path == "/api/state" {
		server.serveState(response)
		return
	}
	if request.Method == http.MethodPut && path == "/api/config" {
		server.updateConfig(response, request)
		return
	}
	if request.Method == http.MethodDelete && strings.HasPrefix(path, "/api/claude/session/") {
		server.stopSession(response, strings.TrimPrefix(path, "/api/claude/session/"))
		return
	}
	if !strings.HasPrefix(path, "/v1/") {
		jsonResponse(response, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	session, ok := server.sessionFor(request)
	if !ok {
		jsonResponse(response, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if request.Method == http.MethodGet && path == "/v1/models" {
		server.serveModels(response)
		return
	}
	if request.Method == http.MethodPost && path == "/v1/messages/count_tokens" {
		body, err := decodeBody(request)
		if err != nil {
			jsonResponse(response, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		route, ok := config.RouteForModel(server.Config(), stringValue(body["model"]))
		if !ok {
			jsonResponse(response, http.StatusNotFound, map[string]any{"error": "model_not_found"})
			return
		}
		server.proxyCountTokens(response, request, route, body)
		return
	}
	if request.Method == http.MethodPost && path == "/v1/messages" {
		server.proxy(response, request, session)
		return
	}
	jsonResponse(response, http.StatusNotFound, map[string]any{"error": "not_found"})
}

func (server *Server) serveManagement(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = io.WriteString(response, webui.Render(webui.Paths{Executable: server.options.ExecutablePath, Command: server.options.CommandPath, Config: server.options.ConfigPath}))
}

func (server *Server) serveState(response http.ResponseWriter) {
	records, err := server.registry.List()
	if err != nil {
		jsonResponse(response, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	server.mu.RLock()
	owned := make(map[string]bool, len(server.sessions))
	for id := range server.sessions {
		owned[id] = true
	}
	server.mu.RUnlock()
	sessions := make([]map[string]any, 0, len(records))
	for _, record := range records {
		sessions = append(sessions, map[string]any{"id": record.ID, "processId": record.ProcessID, "startedAt": record.StartedAt, "routerPid": record.RouterPID, "routerId": record.RouterID, "owned": owned[record.ID]})
	}
	discover := server.options.DiscoverClaude
	if discover == nil {
		discover = claude.Discover
	}
	discovery, discoveryErr := discover(server.Config().Claude.Executable)
	var discoveryValue any
	if discoveryErr == nil {
		discoveryValue = discovery
	}
	jsonResponse(response, http.StatusOK, map[string]any{"config": config.WithoutSecrets(server.Config()), "claudeDiscovery": discoveryValue, "sessions": sessions})
}

func (server *Server) updateConfig(response http.ResponseWriter, request *http.Request) {
	body, err := decodeBody(request)
	if err != nil {
		jsonResponse(response, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	current := server.Config()
	bytes, _ := json.Marshal(current)
	var root map[string]any
	_ = json.Unmarshal(bytes, &root)
	for key := range body {
		if key != "claude" && key != "providers" && key != "models" {
			jsonResponse(response, http.StatusBadRequest, map[string]any{"error": "配置字段不支持：" + key})
			return
		}
	}
	if value, ok := body["claude"]; ok {
		claudeValue, ok := objectValue(value)
		if !ok {
			jsonResponse(response, http.StatusBadRequest, map[string]any{"error": "claude 必须是对象"})
			return
		}
		root["claude"] = mergeJSONMaps(mapValue(root["claude"]), claudeValue)
	}
	if value, ok := body["models"]; ok {
		if _, ok := value.([]any); !ok {
			jsonResponse(response, http.StatusBadRequest, map[string]any{"error": "models 必须是数组"})
			return
		}
		root["models"] = value
	}
	if value, ok := body["providers"]; ok {
		next, ok := objectValue(value)
		if !ok {
			jsonResponse(response, http.StatusBadRequest, map[string]any{"error": "providers 必须是对象"})
			return
		}
		merged := make(map[string]any, len(next))
		previous := current.Providers
		for id, raw := range next {
			provider, ok := objectValue(raw)
			if !ok {
				jsonResponse(response, http.StatusBadRequest, map[string]any{"error": "Provider " + id + " 必须是对象"})
				return
			}
			if _, exists := provider["hasApiKey"]; exists {
				jsonResponse(response, http.StatusBadRequest, map[string]any{"error": "Provider " + id + " 不接受 hasApiKey"})
				return
			}
			if _, exists := provider["configured"]; exists {
				jsonResponse(response, http.StatusBadRequest, map[string]any{"error": "Provider " + id + " 不接受 configured"})
				return
			}
			if _, has := provider["apiKey"]; !has && previous[id].APIKey != "" {
				provider["apiKey"] = previous[id].APIKey
			}
			merged[id] = provider
		}
		root["providers"] = merged
	}
	candidate, _ := json.Marshal(root)
	next, err := config.Parse(candidate)
	if err != nil {
		jsonResponse(response, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := config.Save(server.options.ConfigPath, next); err != nil {
		jsonResponse(response, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	server.mu.Lock()
	server.value = next
	server.mu.Unlock()
	if server.options.OnConfigChanged != nil {
		server.options.OnConfigChanged(cloneConfig(next))
	}
	jsonResponse(response, http.StatusOK, map[string]any{"config": config.WithoutSecrets(next)})
}

func (server *Server) stopSession(response http.ResponseWriter, id string) {
	decoded, _ := url.PathUnescape(id)
	server.mu.RLock()
	session, ok := server.sessions[decoded]
	server.mu.RUnlock()
	if !ok {
		jsonResponse(response, http.StatusConflict, map[string]any{"error": "session_owned_by_other_router"})
		return
	}
	var err error
	if server.options.OnStopClaude != nil {
		err = server.options.OnStopClaude(decoded)
	} else if session.Child != nil {
		err = session.Child.Stop()
	}
	if err != nil {
		jsonResponse(response, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	server.Remove(decoded)
	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) serveModels(response http.ResponseWriter) {
	rows := config.BuildRows(server.Config())
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, map[string]any{"type": "model", "id": row.Value, "display_name": row.Label})
	}
	jsonResponse(response, http.StatusOK, map[string]any{"data": data, "has_more": false})
}

func (server *Server) proxyCountTokens(response http.ResponseWriter, request *http.Request, route config.Route, body map[string]any) {
	body["model"] = route.Model.UpstreamModel
	payload, err := json.Marshal(body)
	if err != nil {
		jsonResponse(response, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	upstream, err := http.NewRequestWithContext(request.Context(), http.MethodPost, upstreamURL(route.Provider.BaseURL, "/v1/messages/count_tokens"), strings.NewReader(string(payload)))
	if err != nil {
		jsonResponse(response, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	upstream.Header = upstreamHeaders(route.Provider, request.Header, false, route.Protocol, route.Model.Provider, "")
	client := server.options.Client
	if client == nil {
		client = &http.Client{Timeout: time.Hour}
	}
	result, err := client.Do(upstream)
	if err != nil {
		jsonResponse(response, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	defer result.Body.Close()
	for name, values := range cloneResponseHeaders(result.Header) {
		for _, value := range values {
			response.Header().Add(name, value)
		}
	}
	response.WriteHeader(result.StatusCode)
	_, _ = io.Copy(response, result.Body)
}

func (server *Server) proxy(response http.ResponseWriter, request *http.Request, session Session) {
	body, err := decodeBody(request)
	if err != nil {
		jsonResponse(response, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	model := stringValue(body["model"])
	route, ok := config.RouteForModel(server.Config(), model)
	if !ok {
		jsonResponse(response, http.StatusNotFound, map[string]any{"error": "model_not_found"})
		return
	}
	upstream, err := ProxyMessages(ProxyRequest{Model: model, UpstreamModel: route.Model.UpstreamModel, ProviderID: route.Model.Provider, SessionID: session.ID, Protocol: route.Protocol, AllowFast: route.Model.Fast != nil && *route.Model.Fast, ForceStreaming: route.Model.Provider == "opencode-go", Body: body, Headers: request.Header.Clone(), Context: request.Context()}, route.Provider, server.options.Client)
	if err != nil {
		jsonResponse(response, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	for name, values := range upstream.Header {
		if hopByHopHeader(name) {
			continue
		}
		for _, value := range values {
			response.Header().Add(name, value)
		}
	}
	response.WriteHeader(upstream.Status)
	if upstream.Stream != nil {
		defer upstream.Stream.Close()
		controller := http.NewResponseController(response)
		buffer := make([]byte, 32*1024)
		for {
			read, readErr := upstream.Stream.Read(buffer)
			if read > 0 {
				if _, writeErr := response.Write(buffer[:read]); writeErr != nil {
					return
				}
				_ = controller.Flush()
			}
			if readErr != nil {
				return
			}
		}
	}
	_, _ = response.Write(upstream.Body)
}

func loopbackHost(value string) bool {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		host = value
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || strings.EqualFold(host, "localhost") || host == "::1"
}
func hopByHopHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Content-Encoding", "Content-Length", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	}
	return false
}
func (server *Server) sessionFor(request *http.Request) (Session, bool) {
	token := request.Header.Get("X-Api-Key")
	server.mu.RLock()
	defer server.mu.RUnlock()
	for _, session := range server.sessions {
		if constantToken(token, session.Token) {
			return session, true
		}
	}
	return Session{}, false
}
func constantToken(actual, expected string) bool {
	if actual == "" || len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
func decodeBody(request *http.Request) (map[string]any, error) {
	reader := io.LimitReader(request.Body, maxBodyBytes+1)
	defer request.Body.Close()
	bytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(bytes) > maxBodyBytes {
		return nil, errors.New("request body too large")
	}
	decoder := json.NewDecoder(strings.NewReader(string(bytes)))
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("request body must be object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("request body must contain one object")
		}
		return nil, err
	}
	return value, nil
}
func jsonResponse(response http.ResponseWriter, status int, value any) {
	bytes, _ := json.Marshal(value)
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Content-Length", fmt.Sprint(len(bytes)))
	response.WriteHeader(status)
	_, _ = response.Write(bytes)
}
func objectValue(value any) (map[string]any, bool) {
	output, ok := value.(map[string]any)
	return output, ok
}
func mergeJSONMaps(left, right map[string]any) map[string]any {
	output := cloneMap(left)
	for key, value := range right {
		output[key] = value
	}
	return output
}
func cloneConfig(value config.Config) config.Config {
	bytes, _ := json.Marshal(value)
	var output config.Config
	_ = json.Unmarshal(bytes, &output)
	return output
}

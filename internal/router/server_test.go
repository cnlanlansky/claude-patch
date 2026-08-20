package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cnlanlansky/claude-patch/internal/claude"
	"github.com/cnlanlansky/claude-patch/internal/config"
)

func testConfig(baseURL string) config.Config {
	return config.Config{
		Claude: config.Claude{},
		Providers: map[string]config.Provider{"demo": {
			Label: "Demo", BaseURL: baseURL, Protocol: config.AnthropicMessages,
			Auth: config.AuthAPIKey, APIKey: "provider-secret-that-must-not-leak",
		}},
		Models: []config.Model{{ID: "claude-router/demo/demo-model", Label: "Demo Model", Provider: "demo", UpstreamModel: "upstream-model"}},
	}
}

func startTestRouter(t *testing.T, value config.Config, upstreamClient *http.Client) *Server {
	t.Helper()
	server, err := Start(Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.json"), InitialConfig: value,
		Client: upstreamClient, Registry: newRegistry(t.TempDir()),
		DiscoverClaude: func(string) (claude.Discovery, error) { return claude.Discovery{}, os.ErrNotExist },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	return server
}

func requestJSON(t *testing.T, method, target string, body any, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return response, bytes
}

func TestRouterLocalManagementAndModelIsolation(t *testing.T) {
	server := startTestRouter(t, testConfig("http://127.0.0.1:1"), nil)
	request, _ := http.NewRequest(http.MethodGet, server.Origin+"/api/state", nil)
	request.Host = "attacker.invalid"
	invalidHost, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	invalidHost.Body.Close()
	if invalidHost.StatusCode != http.StatusForbidden {
		t.Fatalf("Host header 未限制：%d", invalidHost.StatusCode)
	}
	response, body := requestJSON(t, http.MethodGet, server.ManagementURL(), nil, nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Claude Patch") || len(response.Cookies()) != 0 {
		t.Fatalf("管理页不能直接访问：%d %s", response.StatusCode, body)
	}
	response, body = requestJSON(t, http.MethodGet, server.Origin+"/api/state", nil, nil)
	if response.StatusCode != http.StatusOK || strings.Contains(string(body), "provider-secret") {
		t.Fatalf("管理状态错误或泄密：%d %s", response.StatusCode, body)
	}
	response, _ = requestJSON(t, http.MethodGet, server.Origin+"/v1/models", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("模型 API 未隔离：%d", response.StatusCode)
	}
	if err := server.Register(Session{ID: "session-1", Token: "session-token", ProcessID: uint32(os.Getpid()), StartedAt: nowISO()}); err != nil {
		t.Fatal(err)
	}
	response, body = requestJSON(t, http.MethodGet, server.Origin+"/v1/models", nil, map[string]string{"X-Api-Key": "session-token"})
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "claude-router/demo/demo-model") {
		t.Fatalf("模型列表错误：%d %s", response.StatusCode, body)
	}
	response, _ = requestJSON(t, http.MethodPost, server.Origin+"/api/claude/start", map[string]any{}, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("旧 Web 启动入口仍存在：%d", response.StatusCode)
	}
}

func TestRouterConfigMergeKeepsSecretsAndRejectsMalformedUpdates(t *testing.T) {
	value := testConfig("http://127.0.0.1:1")
	server := startTestRouter(t, value, nil)
	var headers map[string]string
	update := map[string]any{"providers": map[string]any{"demo": map[string]any{
		"label": "Renamed", "baseUrl": "https://provider.example.com", "protocol": "anthropic-messages", "auth": "x-api-key",
	}}}
	response, body := requestJSON(t, http.MethodPut, server.Origin+"/api/config", update, headers)
	if response.StatusCode != http.StatusOK || strings.Contains(string(body), "provider-secret") || server.Config().Providers["demo"].APIKey != value.Providers["demo"].APIKey {
		t.Fatalf("配置 merge 泄密或丢 key：%d %s %+v", response.StatusCode, body, server.Config().Providers["demo"])
	}
	for _, malformed := range []map[string]any{
		{"providers": "bad"}, {"claude": []any{}}, {"models": map[string]any{}},
		{"providers": map[string]any{"demo": map[string]any{"hasApiKey": true}}},
		{"providers": map[string]any{"demo": map[string]any{"configured": true}}},
	} {
		response, _ = requestJSON(t, http.MethodPut, server.Origin+"/api/config", malformed, headers)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("畸形配置未拒绝：%v -> %d", malformed, response.StatusCode)
		}
	}
	saved, err := os.ReadFile(server.options.ConfigPath)
	if err != nil || !strings.Contains(string(saved), "provider-secret") {
		t.Fatalf("配置未保存完整 secret：%v %s", err, saved)
	}
	response, _ = requestJSON(t, http.MethodPut, server.Origin+"/api/config", map[string]any{"providers": map[string]any{}}, headers)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("仍被模型引用的 Provider 可被删除：%d", response.StatusCode)
	}
	response, body = requestJSON(t, http.MethodPut, server.Origin+"/api/config", map[string]any{"models": []any{}, "providers": map[string]any{}}, headers)
	if response.StatusCode != http.StatusOK || len(server.Config().Providers) != 0 || len(server.Config().Models) != 0 {
		t.Fatalf("Provider 删除未生效：%d %s %+v", response.StatusCode, body, server.Config())
	}
}

func TestRouterProxyPreservesUpstreamErrorsAndHeaders(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("X-Upstream", "yes")
				response.Header().Set("Connection", "X-Hop-Secret")
				response.Header().Set("X-Hop-Secret", "must-not-forward")
				response.Header().Set("Content-Encoding", "identity")
				response.WriteHeader(status)
				_, _ = io.WriteString(response, fmt.Sprintf(`{"status":%d}`, status))
			}))
			defer upstream.Close()
			server := startTestRouter(t, testConfig(upstream.URL), upstream.Client())
			if err := server.Register(Session{ID: "session", Token: "token", ProcessID: uint32(os.Getpid()), StartedAt: nowISO()}); err != nil {
				t.Fatal(err)
			}
			response, body := requestJSON(t, http.MethodPost, server.Origin+"/v1/messages", map[string]any{"model": "claude-router/demo/demo-model", "messages": []any{}, "stream": false}, map[string]string{"X-Api-Key": "token"})
			expected := fmt.Sprintf(`{"status":%d}`, status)
			if response.StatusCode != status || response.Header.Get("X-Upstream") != "yes" || response.Header.Get("X-Hop-Secret") != "" || response.Header.Get("Content-Encoding") != "" || string(body) != expected {
				t.Fatalf("上游响应未原样透传：%d %v %s", response.StatusCode, response.Header, body)
			}
		})
	}
}

func TestRouterStreamsAnthropicUpstreamBeforeCompletion(t *testing.T) {
	firstWritten := make(chan struct{})
	finish := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.WriteString(response, "data: first\n\n")
		response.(http.Flusher).Flush()
		close(firstWritten)
		<-finish
		_, _ = io.WriteString(response, "data: second\n\n")
	}))
	defer upstream.Close()
	server := startTestRouter(t, testConfig(upstream.URL), upstream.Client())
	if err := server.Register(Session{ID: "stream", Token: "stream-token", ProcessID: uint32(os.Getpid()), StartedAt: nowISO()}); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, server.Origin+"/v1/messages", strings.NewReader(`{"model":"claude-router/demo/demo-model","messages":[],"stream":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Api-Key", "stream-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-firstWritten
	first := make([]byte, len("data: first\n\n"))
	read := make(chan error, 1)
	go func() { _, err := io.ReadFull(response.Body, first); read <- err }()
	select {
	case err := <-read:
		if err != nil || string(first) != "data: first\n\n" {
			t.Fatalf("首个流式块错误：%v %q", err, first)
		}
	case <-time.After(time.Second):
		t.Fatal("Router 在上游结束前未转发首个流式块")
	}
	close(finish)
	rest, err := io.ReadAll(response.Body)
	if err != nil || string(rest) != "data: second\n\n" {
		t.Fatalf("剩余流式块错误：%v %q", err, rest)
	}
}

func TestRouterPropagatesClientCancellation(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.WriteString(response, "data: first\n\n")
		response.(http.Flusher).Flush()
		close(started)
		<-request.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	server := startTestRouter(t, testConfig(upstream.URL), upstream.Client())
	if err := server.Register(Session{ID: "cancel", Token: "cancel-token", ProcessID: uint32(os.Getpid()), StartedAt: nowISO()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.Origin+"/v1/messages", strings.NewReader(`{"model":"claude-router/demo/demo-model","messages":[],"stream":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Api-Key", "cancel-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("上游流未开始")
	}
	first := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(response.Body, first); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("客户端取消未传播到上游")
	}
}

func TestProxyMessagesPropagatesCancellation(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	client := handlerClient(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
		close(canceled)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ProxyMessages(ProxyRequest{
			Model: "router-model", Protocol: config.AnthropicMessages,
			Body: map[string]any{"messages": []any{}, "stream": true}, Context: ctx,
		}, config.Provider{Label: "Demo", BaseURL: "https://provider.invalid", Protocol: config.AnthropicMessages, Auth: config.AuthNone}, client)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("上游请求未开始")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("请求取消未传播到上游")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("取消后上游请求未结束")
	}
}

func TestRouterCountTokensProxiesUpstream(t *testing.T) {
	var upstreamModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages/count_tokens" || request.Header.Get("X-Api-Key") != "provider-secret-that-must-not-leak" {
			t.Errorf("count_tokens 上游请求错误：%s %v", request.URL.Path, request.Header)
		}
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		upstreamModel = stringValue(body["model"])
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"input_tokens":42}`)
	}))
	defer upstream.Close()
	server := startTestRouter(t, testConfig(upstream.URL), upstream.Client())
	if err := server.Register(Session{ID: "tokens", Token: "tokens-token", ProcessID: uint32(os.Getpid()), StartedAt: nowISO()}); err != nil {
		t.Fatal(err)
	}
	response, body := requestJSON(t, http.MethodPost, server.Origin+"/v1/messages/count_tokens", map[string]any{"model": "claude-router/demo/demo-model", "messages": []any{}}, map[string]string{"X-Api-Key": "tokens-token"})
	if response.StatusCode != http.StatusOK || string(body) != `{"input_tokens":42}` || upstreamModel != "upstream-model" {
		t.Fatalf("count_tokens 未透明转发：%d %s %q", response.StatusCode, body, upstreamModel)
	}
}

func TestRouterRejectsRegistrationAfterStop(t *testing.T) {
	server := startTestRouter(t, testConfig("http://127.0.0.1:1"), nil)
	if err := server.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := server.Register(Session{ID: "late", Token: "token", ProcessID: uint32(os.Getpid()), StartedAt: nowISO()}); err == nil {
		t.Fatal("Router 停止后仍接受 session")
	}
}

func TestRouterDisabledModelAndOwnedSessionStop(t *testing.T) {
	value := testConfig("http://127.0.0.1:1")
	disabled := false
	value.Models[0].Enabled = &disabled
	stopped := ""
	server, err := Start(Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.json"), InitialConfig: value,
		Registry: newRegistry(t.TempDir()), DiscoverClaude: func(string) (claude.Discovery, error) { return claude.Discovery{}, os.ErrNotExist },
		OnStopClaude: func(id string) error { stopped = id; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	if err := server.Register(Session{ID: "owned", Token: "token", ProcessID: uint32(os.Getpid()), StartedAt: nowISO()}); err != nil {
		t.Fatal(err)
	}
	response, body := requestJSON(t, http.MethodGet, server.Origin+"/v1/models", nil, map[string]string{"X-Api-Key": "token"})
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"data":[]`) {
		t.Fatalf("停用模型仍暴露：%d %s", response.StatusCode, body)
	}
	response, _ = requestJSON(t, http.MethodDelete, server.Origin+"/api/claude/session/other", nil, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("跨 Router stop 未隔离：%d", response.StatusCode)
	}
	response, _ = requestJSON(t, http.MethodDelete, server.Origin+"/api/claude/session/owned", nil, nil)
	if response.StatusCode != http.StatusNoContent || stopped != "owned" {
		t.Fatalf("owned stop 失败：%d %q", response.StatusCode, stopped)
	}
}

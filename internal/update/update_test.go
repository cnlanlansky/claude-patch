package update

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func updateClient(status int, body string, check func(*http.Request)) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if check != nil {
			check(request)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})}
}

func TestCheckComparesValidatedReleaseVersions(t *testing.T) {
	tests := []struct {
		name        string
		current     string
		latest      string
		available   bool
		ahead       bool
		development bool
	}{
		{name: "newer", current: "v1.9.0", latest: "v1.10.0", available: true},
		{name: "same", current: "v1.10.0", latest: "v1.10.0"},
		{name: "older remote", current: "v1.10.1", latest: "v1.10.0", ahead: true},
		{name: "development", current: "dev", latest: "v1.10.0", development: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Check(context.Background(), test.current, updateClient(http.StatusOK, `{"tag_name":"`+test.latest+`"}`, nil))
			if err != nil {
				t.Fatal(err)
			}
			if result.UpdateAvailable != test.available || result.CurrentAhead != test.ahead || result.DevelopmentBuild != test.development || result.ReleaseURL != releaseURLBase+test.latest {
				t.Fatalf("版本比较错误：%+v", result)
			}
		})
	}
}

func TestCheckValidatesRequestAndErrors(t *testing.T) {
	client := updateClient(http.StatusOK, `{"tag_name":"v1.2.3"}`, func(request *http.Request) {
		if request.Method != http.MethodGet || request.URL.String() != latestReleaseURL || request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("X-GitHub-Api-Version") != "2022-11-28" || request.Header.Get("User-Agent") != "Claude-Patch/v1.0.0" || request.Header.Get("Authorization") != "" {
			t.Fatalf("更新请求错误：%v", request)
		}
		if _, ok := request.Context().Deadline(); !ok {
			t.Fatal("更新请求没有超时边界")
		}
	})
	if _, err := Check(context.Background(), "v1.0.0", client); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "http error", status: http.StatusForbidden, body: `{}`},
		{name: "bad json", status: http.StatusOK, body: `[`},
		{name: "bad tag", status: http.StatusOK, body: `{"tag_name":"latest"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Check(context.Background(), "v1.0.0", updateClient(test.status, test.body, nil)); err == nil {
				t.Fatal("无效响应未失败")
			}
		})
	}
}

func TestCheckFallsBackToWebReleaseAfterForbidden(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			if request.URL.String() != latestReleaseURL {
				t.Fatalf("API 请求地址错误：%s", request.URL)
			}
			return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"message":"API rate limit exceeded"}`)), Header: make(http.Header), Request: request}, nil
		case 2:
			if request.URL.String() != latestReleaseWebURL || request.Header.Get("Authorization") != "" {
				t.Fatalf("网页回退请求错误：%v", request)
			}
			return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Location": []string{releaseURLBase + "v1.2.3"}}, Request: request}, nil
		default:
			t.Fatalf("请求次数过多：%d", requests)
			return nil, nil
		}
	})}
	result, err := Check(context.Background(), "v1.0.0", client)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || result.LatestVersion != "v1.2.3" || !result.UpdateAvailable || result.ReleaseURL != releaseURLBase+"v1.2.3" {
		t.Fatalf("网页回退结果错误：请求 %d 次，结果 %+v", requests, result)
	}
}

func TestCheckRejectsInvalidWebReleaseRedirect(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		location string
	}{
		{name: "missing location", status: http.StatusFound},
		{name: "wrong host", status: http.StatusFound, location: "https://example.com/cnlanlansky/claude-patch/releases/tag/v1.2.3"},
		{name: "wrong repository", status: http.StatusFound, location: "https://github.com/other/project/releases/tag/v1.2.3"},
		{name: "query", status: http.StatusFound, location: releaseURLBase + "v1.2.3?download=1"},
		{name: "invalid tag", status: http.StatusFound, location: releaseURLBase + "latest"},
		{name: "not redirect", status: http.StatusOK, location: releaseURLBase + "v1.2.3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				if requests == 1 {
					return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header), Request: request}, nil
				}
				if requests > 2 {
					t.Fatalf("请求次数过多：%d", requests)
				}
				header := make(http.Header)
				if test.location != "" {
					header.Set("Location", test.location)
				}
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader("")), Header: header, Request: request}, nil
			})}
			if _, err := Check(context.Background(), "v1.0.0", client); err == nil {
				t.Fatal("非法网页回退未失败")
			}
			if requests != 2 {
				t.Fatalf("网页回退请求次数错误：%d", requests)
			}
		})
	}
}

func TestCheckDoesNotFallbackForOtherHTTPErrors(t *testing.T) {
	requests := 0
	client := updateClient(http.StatusBadGateway, `{}`, func(request *http.Request) {
		requests++
	})
	if _, err := Check(context.Background(), "v1.0.0", client); err == nil {
		t.Fatal("非 403 响应未失败")
	}
	if requests != 1 {
		t.Fatalf("非 403 响应错误触发回退：请求 %d 次", requests)
	}
}
func TestCheckRejectsOversizedBodyAndRedirect(t *testing.T) {
	if _, err := Check(context.Background(), "v1.0.0", updateClient(http.StatusOK, strings.Repeat("x", maxReleaseBody+1), nil)); err == nil {
		t.Fatal("超大响应未失败")
	}
	redirectClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: request}, nil
	})}
	if _, err := Check(context.Background(), "v1.0.0", redirectClient); err == nil {
		t.Fatal("重定向未失败")
	}
}

func TestCheckHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Check(ctx, "v1.0.0", updateClient(http.StatusOK, `{"tag_name":"v1.0.1"}`, nil))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误错误：%v", err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err = Check(ctx, "v1.0.0", updateClient(http.StatusOK, `{"tag_name":"v1.0.1"}`, nil))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("超时错误错误：%v", err)
	}
}

package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	latestReleaseURL    = "https://api.github.com/repos/cnlanlansky/claude-patch/releases/latest"
	latestReleaseWebURL = "https://github.com/cnlanlansky/claude-patch/releases/latest"
	releaseURLBase      = "https://github.com/cnlanlansky/claude-patch/releases/tag/"
	requestTimeout      = 5 * time.Second
	maxReleaseBody      = 1 << 20
)

var releaseTagPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

type Result struct {
	CurrentVersion   string
	LatestVersion    string
	ReleaseURL       string
	UpdateAvailable  bool
	CurrentAhead     bool
	DevelopmentBuild bool
}

type releaseResponse struct {
	TagName string `json:"tag_name"`
}

type versionParts struct {
	major uint64
	minor uint64
	patch uint64
}

func Check(ctx context.Context, current string, client *http.Client) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Claude-Patch/"+current)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if err := requestContext.Err(); err != nil {
		return Result{}, err
	}

	httpClient := client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	copyClient := *httpClient
	if copyClient.Timeout == 0 {
		copyClient.Timeout = requestTimeout
	}
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return Result{}, errors.New("检查更新超时")
		}
		if errors.Is(err, context.Canceled) || errors.Is(requestContext.Err(), context.Canceled) {
			return Result{}, context.Canceled
		}
		return Result{}, fmt.Errorf("无法连接 GitHub：%w", err)
	}
	if requestErr := requestContext.Err(); requestErr != nil {
		_ = response.Body.Close()
		if errors.Is(requestErr, context.DeadlineExceeded) {
			return Result{}, errors.New("检查更新超时")
		}
		return Result{}, context.Canceled
	}
	if response.StatusCode == http.StatusForbidden {
		_ = response.Body.Close()
		latest, latestParts, fallbackErr := checkWebRelease(requestContext, &copyClient, current)
		if fallbackErr != nil {
			if errors.Is(fallbackErr, context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded) {
				return Result{}, errors.New("检查更新超时")
			}
			if errors.Is(fallbackErr, context.Canceled) || errors.Is(requestContext.Err(), context.Canceled) {
				return Result{}, context.Canceled
			}
			return Result{}, fmt.Errorf("GitHub 更新检查失败（HTTP 403）：%w", fallbackErr)
		}
		return resultForRelease(current, latest, latestParts)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("GitHub 更新检查失败（HTTP %d）", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseBody+1))
	if err != nil {
		return Result{}, fmt.Errorf("读取 GitHub Release 失败：%w", err)
	}
	if len(body) > maxReleaseBody {
		return Result{}, errors.New("GitHub Release 响应过大")
	}
	var release releaseResponse
	if err := json.Unmarshal(body, &release); err != nil {
		return Result{}, errors.New("GitHub 返回了无效的 Release 信息")
	}
	latest, latestParts, err := parseReleaseTag(release.TagName)
	if err != nil {
		return Result{}, err
	}
	return resultForRelease(current, latest, latestParts)
}

func checkWebRelease(ctx context.Context, client *http.Client, current string) (string, versionParts, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseWebURL, nil)
	if err != nil {
		return "", versionParts{}, err
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("User-Agent", "Claude-Patch/"+current)
	response, err := client.Do(request)
	if err != nil {
		return "", versionParts{}, err
	}
	if requestErr := ctx.Err(); requestErr != nil {
		_ = response.Body.Close()
		return "", versionParts{}, requestErr
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusMultipleChoices || response.StatusCode >= http.StatusBadRequest {
		return "", versionParts{}, fmt.Errorf("GitHub Releases 页面未返回重定向（HTTP %d）", response.StatusCode)
	}
	return parseReleaseRedirect(response.Header.Get("Location"))
}

func parseReleaseRedirect(value string) (string, versionParts, error) {
	redirect, err := url.Parse(value)
	if err != nil || !strings.EqualFold(redirect.Scheme, "https") || !strings.EqualFold(redirect.Host, "github.com") || redirect.User != nil || redirect.RawQuery != "" || redirect.ForceQuery || redirect.Fragment != "" || redirect.Opaque != "" {
		return "", versionParts{}, errors.New("GitHub Releases 重定向地址无效")
	}
	const releasePathPrefix = "/cnlanlansky/claude-patch/releases/tag/"
	if !strings.HasPrefix(redirect.Path, releasePathPrefix) {
		return "", versionParts{}, errors.New("GitHub Releases 重定向地址无效")
	}
	tag := strings.TrimPrefix(redirect.Path, releasePathPrefix)
	_, parts, err := parseReleaseTag(tag)
	if err != nil {
		return "", versionParts{}, errors.New("GitHub Releases 重定向版本无效")
	}
	return tag, parts, nil
}

func resultForRelease(current, latest string, latestParts versionParts) (Result, error) {
	result := Result{CurrentVersion: current, LatestVersion: latest, ReleaseURL: releaseURLBase + latest}
	if current == "dev" {
		result.DevelopmentBuild = true
		return result, nil
	}
	currentParts, err := parseVersion(current)
	if err != nil {
		return Result{}, fmt.Errorf("当前应用版本无效：%w", err)
	}
	comparison := compareVersions(currentParts, latestParts)
	result.UpdateAvailable = comparison < 0
	result.CurrentAhead = comparison > 0
	return result, nil
}

func parseReleaseTag(value string) (string, versionParts, error) {
	parts := releaseTagPattern.FindStringSubmatch(value)
	if len(parts) != 4 {
		return "", versionParts{}, errors.New("GitHub 返回了无效的 Release 版本")
	}
	parsed := make([]uint64, 3)
	for index, value := range parts[1:] {
		part, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return "", versionParts{}, errors.New("GitHub 返回了无效的 Release 版本")
		}
		parsed[index] = part
	}
	return value, versionParts{major: parsed[0], minor: parsed[1], patch: parsed[2]}, nil
}

func parseVersion(value string) (versionParts, error) {
	_, parsed, err := parseReleaseTag(value)
	return parsed, err
}

func compareVersions(left, right versionParts) int {
	if left.major != right.major {
		if left.major < right.major {
			return -1
		}
		return 1
	}
	if left.minor != right.minor {
		if left.minor < right.minor {
			return -1
		}
		return 1
	}
	if left.patch < right.patch {
		return -1
	}
	if left.patch > right.patch {
		return 1
	}
	return 0
}

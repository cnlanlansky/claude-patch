package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type SearchOptions struct {
	AllowedDomains []string
	BlockedDomains []string
	NumResults     int
}

type SearchResult struct {
	Title            string `json:"title"`
	URL              string `json:"url"`
	Snippet          string `json:"snippet,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
	PageAge          string `json:"page_age,omitempty"`
}

type SearchExecutor func(context.Context, string, SearchOptions) ([]SearchResult, error)

type SearchError struct {
	Code string
	Err  error
}

func (err *SearchError) Error() string {
	if err == nil || err.Err == nil {
		return ""
	}
	return err.Err.Error()
}

func (err *SearchError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

type searchResolution struct {
	ID, Error, ErrorCode string
	Input                map[string]any
	Results              []SearchResult
	SearchResultContent  []any
}

func resolveSearches(ctx context.Context, normalized NormalizedResponse, executor SearchExecutor) []searchResolution {
	var output []searchResolution
	if executor == nil {
		executor = SearchWeb
	}
	for _, tool := range normalized.Tools {
		if !tool.ServerTool {
			continue
		}
		var input map[string]any
		_ = json.Unmarshal([]byte(tool.Arguments), &input)
		options := SearchOptions{AllowedDomains: stringsFrom(input["allowed_domains"]), BlockedDomains: stringsFrom(input["blocked_domains"]), NumResults: int(numberValue(input["numResults"]))}
		results, err := executor(ctx, stringValue(input["query"]), options)
		var resultContent []any
		if results != nil {
			resultContent = searchResultBlocks(results)
		}
		resolution := searchResolution{ID: tool.ID, Input: input, Results: results, SearchResultContent: resultContent}
		if err != nil {
			resolution.Error = err.Error()
			resolution.ErrorCode = searchErrorCode(err)
			var searchErr *SearchError
			if errors.As(err, &searchErr) && searchErr.Code != "" {
				resolution.ErrorCode = searchErr.Code
			}
		}
		output = append(output, resolution)
	}
	return output
}

func searchResultBlocks(results []SearchResult) []any {
	blocks := make([]any, 0, len(results))
	for _, result := range results {
		block := map[string]any{"type": "web_search_result", "title": result.Title, "url": result.URL, "encrypted_content": result.EncryptedContent, "page_age": result.PageAge}
		blocks = append(blocks, block)
	}
	return blocks
}

func searchErrorCode(err error) string {
	if err == nil {
		return "unavailable"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "query too long"):
		return "query_too_long"
	case strings.Contains(message, "too large"):
		return "request_too_large"
	case strings.Contains(message, "too many requests"), strings.Contains(message, "http 429"):
		return "too_many_requests"
	default:
		return "unavailable"
	}
}

func SearchWeb(ctx context.Context, query string, options SearchOptions) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []SearchResult{}, nil
	}
	if len(query) > 2048 {
		return nil, errors.New("web search query too long")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://html.duckduckgo.com/html/?q="+url.QueryEscape(query), nil)
	request.Header.Set("User-Agent", "Mozilla/5.0 (Claude Patch)")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusTooManyRequests {
			return nil, &SearchError{Code: "too_many_requests", Err: fmt.Errorf("search upstream returned HTTP %d", response.StatusCode)}
		}
		return nil, fmt.Errorf("search upstream returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 4*1024*1024 {
		return nil, errors.New("web search response too large")
	}
	return parseSearchHTML(string(body), options), nil
}

var resultLink = regexp.MustCompile(`(?is)<a\b([^>]*\bclass=["'][^"']*\bresult__a\b[^"']*[^>]*)>(.*?)</a>`)
var resultSnippet = regexp.MustCompile(`(?is)<a\b[^>]*\bclass=["'][^"']*\bresult__snippet\b[^"']*["'][^>]*>(.*?)</a>`)
var hrefAttr = regexp.MustCompile(`(?i)\bhref=["']([^"']+)["']`)
var htmlTag = regexp.MustCompile(`<[^>]*>`)

func parseSearchHTML(body string, options SearchOptions) []SearchResult {
	limit := options.NumResults
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	snippetMatches := resultSnippet.FindAllStringSubmatch(body, -1)
	snippets := make([]string, len(snippetMatches))
	for index, match := range snippetMatches {
		snippets[index] = cleanHTML(match[1])
	}
	var output []SearchResult
	for _, match := range resultLink.FindAllStringSubmatch(body, -1) {
		href := hrefAttr.FindStringSubmatch(match[1])
		if len(href) < 2 {
			continue
		}
		parsed, err := url.Parse(href[1])
		if err != nil {
			continue
		}
		if redirect := parsed.Query().Get("uddg"); redirect != "" {
			parsed, err = url.Parse(redirect)
			if err != nil {
				continue
			}
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" || domainMatch(parsed, options.BlockedDomains) || len(options.AllowedDomains) > 0 && !domainMatch(parsed, options.AllowedDomains) {
			continue
		}
		title := cleanHTML(match[2])
		if title == "" {
			continue
		}
		result := SearchResult{Title: title, URL: parsed.String()}
		if len(output) < len(snippets) {
			result.Snippet = snippets[len(output)]
		}
		output = append(output, result)
		if len(output) >= limit {
			break
		}
	}
	return output
}

func cleanHTML(value string) string {
	return strings.Join(strings.Fields(html.UnescapeString(htmlTag.ReplaceAllString(value, " "))), " ")
}

func domainMatch(value *url.URL, domains []string) bool {
	host := strings.ToLower(value.Hostname())
	for _, domain := range domains {
		domain = strings.TrimLeft(strings.ToLower(strings.TrimSpace(domain)), ".")
		if domain != "" && (host == domain || strings.HasSuffix(host, "."+domain)) {
			return true
		}
	}
	return false
}

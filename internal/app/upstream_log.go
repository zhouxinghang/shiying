package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type upstreamLogKey struct{}

type upstreamLog struct {
	started time.Time
	request *http.Request
}

var logURLPattern = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
var logSecretPattern = regexp.MustCompile(`(?i)((?:sessionid(?:_ss)?|sid_tt|msToken|a_bogus|x-bogus|uifid\w*|s_v_web_id|verifyFp|fp|\w*token|\w*signature|cookie|authorization)["']?\s*[:=]\s*)["']?[^\s"'&,;<>]+`)
var logTitlePattern = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title\s*>`)

// Query strings can contain login credentials and signed media addresses.
// Keep only the endpoint here; useful, validated parameters are logged separately.
func upstreamLogURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

func (l *upstreamLog) text(value string) string {
	value = logURLPattern.ReplaceAllStringFunc(value, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[invalid URL]"
		}
		return upstreamLogURL(u)
	})
	// Redact credentials even when an upstream message echoes a value without
	// its field name. Include generated query tokens and signature headers.
	secrets := []string{l.request.Header.Get("Cookie")}
	for _, cookie := range l.request.Cookies() {
		secrets = append(secrets, cookie.Value)
	}
	for key, values := range l.request.URL.Query() {
		if logSecretPattern.MatchString(key + "=value") {
			secrets = append(secrets, values...)
		}
	}
	for _, key := range []string{"uifid", "x-secsdk-web-signature", "x-secsdk-web-expire", "Authorization"} {
		secrets = append(secrets, l.request.Header.Get(key))
	}
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
			value = strings.ReplaceAll(value, url.QueryEscape(secret), "[REDACTED]")
		}
	}
	value = logSecretPattern.ReplaceAllString(value, "${1}[REDACTED]")
	value = strings.Join(strings.Fields(value), " ")
	if runes := []rune(value); len(runes) > 512 {
		value = string(runes[:512]) + "…"
	}
	return value
}

func (l *upstreamLog) failure(stage string, resp *http.Response, body []byte, err error) {
	req := l.request
	attrs := []any{"stage", stage, "method", req.Method, "url", upstreamLogURL(req.URL), "elapsed_ms", time.Since(l.started).Milliseconds(), "cookie_configured", req.Header.Get("Cookie") != ""}
	params := map[string]string{}
	for key, values := range req.URL.Query() {
		if len(values) != 1 {
			continue
		}
		value := values[0]
		switch key {
		case "aweme_id", "item_ids", "modal_id", "vid", "cursor", "max_cursor", "count":
			if idPattern.MatchString(value) {
				params[key] = value
			}
		case "sec_user_id", "sec_uid":
			if authorUIDPattern.MatchString(value) {
				params[key] = value
			}
		}
	}
	if len(params) > 0 {
		attrs = append(attrs, "params", params)
	}
	if err != nil {
		attrs = append(attrs, "error", l.text(err.Error()), "error_type", fmt.Sprintf("%T", err))
		if classified, ok := err.(*ParseError); ok {
			attrs = append(attrs, "error_code", classified.Code)
		}
	}
	if resp != nil {
		attrs = append(attrs, "http_status", resp.StatusCode, "content_type", l.text(resp.Header.Get("Content-Type")), "content_length", resp.ContentLength)
		if resp.Request != nil && upstreamLogURL(resp.Request.URL) != upstreamLogURL(req.URL) {
			attrs = append(attrs, "final_url", upstreamLogURL(resp.Request.URL))
		}
		for _, key := range []string{"X-Tt-Logid", "X-Request-Id", "Retry-After"} {
			if value := resp.Header.Get(key); value != "" {
				attrs = append(attrs, strings.ToLower(key), l.text(value))
			}
		}
	}
	if body != nil {
		attrs = append(attrs, "body_bytes", len(body))
		trimmed := bytes.TrimSpace(body)
		format := "text"
		var root map[string]any
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		if dec.Decode(&root) == nil && root != nil {
			format = "json"
			// Never dump complete responses: self/profile and feed payloads may
			// contain private account data and signed URLs.
			if code, ok := root["status_code"]; ok {
				attrs = append(attrs, "upstream_status_code", number(code))
			}
			for _, key := range []string{"status_msg", "status_message", "message"} {
				if value, ok := root[key].(string); ok && value != "" {
					attrs = append(attrs, "upstream_"+key, l.text(value))
				}
			}
		} else if len(trimmed) == 0 {
			format = "empty"
		} else if trimmed[0] == '<' {
			format = "html"
			if title := logTitlePattern.FindSubmatch(trimmed); len(title) > 1 {
				attrs = append(attrs, "page_title", l.text(html.UnescapeString(string(title[1]))))
			}
		} else if trimmed[0] == '{' || trimmed[0] == '[' {
			format = "invalid_json"
		}
		attrs = append(attrs, "response_format", format)
	}
	slog.WarnContext(req.Context(), "抖音请求失败", attrs...)
}

func logUpstreamFailure(resp *http.Response, stage string, body []byte, err error) {
	if resp == nil || resp.Request == nil {
		return
	}
	if l, ok := resp.Request.Context().Value(upstreamLogKey{}).(*upstreamLog); ok {
		l.failure(stage, resp, body, err)
	}
}

// Call only when the caller will discard the failed response. Bound diagnostics
// so an error page cannot cause an unbounded read or an oversized log entry.
func logUpstreamHTTPFailure(resp *http.Response) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	logUpstreamFailure(resp, "http_status", body, err)
}

func readUpstreamBody(resp *http.Response, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		logUpstreamFailure(resp, "read_body", body, err)
		return nil, err
	}
	if int64(len(body)) > limit {
		err = fmt.Errorf("响应超过 %d 字节上限", limit)
		logUpstreamFailure(resp, "response_too_large", nil, err)
		return nil, err
	}
	return body, nil
}

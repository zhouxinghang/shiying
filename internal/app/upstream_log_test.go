package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func captureUpstreamLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &output
}

func upstreamLogRecord(t *testing.T, output *bytes.Buffer) map[string]any {
	t.Helper()
	var record map[string]any
	dec := json.NewDecoder(bytes.NewReader(output.Bytes()))
	if err := dec.Decode(&record); err != nil {
		t.Fatalf("missing failure log: %v; output=%s", err, output)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("expected exactly one failure log: %s", output)
	}
	for _, key := range []string{"stage", "method", "url", "elapsed_ms", "cookie_configured"} {
		if _, ok := record[key]; !ok {
			t.Errorf("missing diagnostic field %s: %s", key, output)
		}
	}
	return record
}

func TestUpstreamNetworkFailureLogRedactsCredentials(t *testing.T) {
	output := captureUpstreamLogs(t)
	raw := "https://www.douyin.com/aweme/v1/web/aweme/detail/?aweme_id=" + fixtureID + "&msToken=private-token&a_bogus=private-signature"
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial tcp: connection refused; sessionid=private-session; %s; private-header-signature", req.URL)
	})}
	_, err := request(context.Background(), client, raw, "sessionid=private-session", pageDomains, http.Header{"X-Secsdk-Web-Signature": {"private-header-signature"}})
	if err == nil {
		t.Fatal("expected network failure")
	}
	record := upstreamLogRecord(t, output)
	if record["stage"] != "request" || record["cookie_configured"] != true || !strings.Contains(record["error"].(string), "connection refused") {
		t.Fatalf("network diagnostics missing: %s", output)
	}
	if record["params"].(map[string]any)["aweme_id"] != fixtureID {
		t.Fatalf("content identifier missing: %s", output)
	}
	for _, secret := range []string{"private-token", "private-signature", "private-session", "private-header-signature"} {
		if strings.Contains(output.String(), secret) {
			t.Errorf("credential leaked: %s", secret)
		}
	}
}

func TestUpstreamAPIFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		stage  string
		format string
		code   string
	}{
		{"unauthorized", 401, `{"status_code":8,"status_msg":"login required"}`, "http_status", "json", "UPSTREAM_RISK_CONTROL"},
		{"forbidden", 403, `{"status_code":8,"status_msg":"verify"}`, "http_status", "json", "UPSTREAM_RISK_CONTROL"},
		{"rate limit", 429, `{"status_code":8,"status_msg":"too many requests"}`, "http_status", "json", "UPSTREAM_RISK_CONTROL"},
		{"server error", 503, `<html><title>服务暂不可用</title></html>`, "http_status", "html", ""},
		{"empty", 200, "", "decode_json", "empty", "UPSTREAM_RISK_CONTROL"},
		{"malformed JSON", 200, `{"status_code":`, "decode_json", "invalid_json", "UPSTREAM_RISK_CONTROL"},
		{"verification page", 200, `<html><title>安全验证</title><script>private-script</script></html>`, "decode_json", "html", "UPSTREAM_RISK_CONTROL"},
		{"risk control", 200, `{"status_code":8,"status_msg":"verify","captcha":true}`, "risk_control", "json", "UPSTREAM_RISK_CONTROL"},
		{"business failure", 200, `{"status_code":123,"status_msg":"upstream error"}`, "business_status", "json", "UPSTREAM_BUSINESS_ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := captureUpstreamLogs(t)
			r := NewResolver(nil)
			r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				resp := response(req, "application/json", []byte(tc.body))
				resp.StatusCode = tc.status
				resp.Header.Set("X-Tt-Logid", "trace-fixture")
				resp.Header.Set("Retry-After", "30")
				resp.Header.Set("Set-Cookie", "sessionid=private-response-cookie")
				return resp, nil
			})
			_, err := r.webJSONWithCookie(context.Background(), http.MethodPost, "/aweme/v1/web/aweme/listcollection/", []queryParam{{"cursor", "20"}, {"count", "20"}}, "sessionid=private-session")
			if err == nil {
				t.Fatal("expected failure")
			}
			var classified *ParseError
			if tc.code != "" && (!errors.As(err, &classified) || classified.Code != tc.code) {
				t.Fatalf("error classification changed: %v", err)
			}
			record := upstreamLogRecord(t, output)
			if record["stage"] != tc.stage || record["method"] != "POST" || record["http_status"] != float64(tc.status) || record["response_format"] != tc.format || record["x-tt-logid"] != "trace-fixture" || record["retry-after"] != "30" {
				t.Fatalf("incomplete diagnostics: %s", output)
			}
			for _, secret := range []string{"private-session", "private-response-cookie", "private-script"} {
				if strings.Contains(output.String(), secret) {
					t.Errorf("private response data leaked: %s", secret)
				}
			}
		})
	}
}

func TestUpstreamResponseLogLimitsAndRedaction(t *testing.T) {
	output := captureUpstreamLogs(t)
	r := NewResolver(nil)
	var signature string
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		signature = req.URL.Query().Get("a_bogus")
		body, _ := json.Marshal(map[string]any{
			"status_code": 8,
			"status_msg":  "verify " + signature + " private-session private-token https://user:private-password@www.douyin.com/check?token=private-query#private-fragment sessionid=other-session " + strings.Repeat("详情", 1000),
			"user":        map[string]string{"bind_phone": "private-phone", "nickname": "private-nickname"},
			"media_url":   "https://v.douyinvod.com/video?signature=private-media-signature",
		})
		return response(req, "application/json", body), nil
	})
	_, err := r.webJSONWithCookie(context.Background(), http.MethodGet, "/aweme/v1/web/user/profile/self/", nil, "sessionid=private-session; msToken=private-token")
	if err == nil {
		t.Fatal("expected risk control")
	}
	record := upstreamLogRecord(t, output)
	if record["upstream_status_code"] != float64(8) || len([]rune(record["upstream_status_msg"].(string))) > 513 {
		t.Fatalf("missing status or unbounded message: %s", output)
	}
	for _, secret := range []string{signature, "private-session", "private-token", "private-password", "private-query", "private-fragment", "other-session", "private-phone", "private-nickname", "private-media-signature"} {
		if strings.Contains(output.String(), secret) {
			t.Errorf("sensitive data leaked: %s", secret)
		}
	}
}

type upstreamErrorReader struct{}

func (upstreamErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestUpstreamBodyReadFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reader io.Reader
		stage  string
	}{
		{"interrupted", upstreamErrorReader{}, "read_body"},
		{"too large", strings.NewReader(strings.Repeat("x", 33)), "response_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := captureUpstreamLogs(t)
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				resp := response(req, "application/json", nil)
				resp.Body = io.NopCloser(tc.reader)
				return resp, nil
			})}
			resp, err := request(context.Background(), client, "https://www.douyin.com/response", "", pageDomains)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if _, err = readUpstreamBody(resp, 32); err == nil {
				t.Fatal("invalid body accepted")
			}
			if record := upstreamLogRecord(t, output); record["stage"] != tc.stage {
				t.Fatalf("wrong stage: %s", output)
			}
		})
	}
}

func TestUpstreamLogsFailedFallbackBeforeSuccessfulParse(t *testing.T) {
	output := captureUpstreamLogs(t)
	r := NewResolver(newStore(t))
	requests := 0
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return response(req, "text/html", []byte(`<html><title>安全验证</title></html>`)), nil
		}
		return response(req, "application/json", []byte(`{"aweme_detail":{"aweme_id":"`+fixtureID+`","desc":"test","status":{},"statistics":{},"author":{"sec_uid":"`+fixtureAuthor+`","nickname":"test"},"video":{"play_addr":{"url_list":["https://v.douyinvod.com/test.mp4"]}}}}`)), nil
	})
	c, err := r.Resolve(context.Background(), "https://www.douyin.com/video/"+fixtureID)
	if err != nil || c.ID != fixtureID || requests != 2 {
		t.Fatalf("fallback changed: id=%s requests=%d err=%v", c.ID, requests, err)
	}
	if record := upstreamLogRecord(t, output); record["stage"] != "parse_content" || record["page_title"] != "安全验证" {
		t.Fatalf("failed fallback diagnostics missing: %s", output)
	}
	output.Reset()
	if _, err := r.Resolve(context.Background(), "https://www.douyin.com/video/"+fixtureID); err != nil || output.Len() != 0 {
		t.Fatalf("successful request produced failure logs: err=%v logs=%s", err, output)
	}
}

func TestUpstreamHTTPDiagnosticReadIsBounded(t *testing.T) {
	output := captureUpstreamLogs(t)
	body := strings.NewReader(strings.Repeat("x", 1<<20))
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp := response(req, "text/plain", nil)
		resp.StatusCode = 503
		resp.Body = io.NopCloser(body)
		return resp, nil
	})}
	resp, err := request(context.Background(), client, "https://www.douyin.com/response", "", pageDomains)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	logUpstreamHTTPFailure(resp)
	if record := upstreamLogRecord(t, output); record["body_bytes"] != float64(4096) || body.Len() != (1<<20)-4096 || output.Len() > 2048 {
		t.Fatalf("error diagnostics read or logged too much: remaining=%d log=%s", body.Len(), output)
	}
}

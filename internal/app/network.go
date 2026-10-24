package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var pageDomains = []string{"douyin.com", "iesdouyin.com", "amemv.com"}

const legacyPlaybackHost = "aweme.snssdk.com"

// Reference: src/dtk/media/domains.py and docker/downloader/safety.go.
var mediaDomains = []string{"douyinvod.com", "zjcdn.com", "douyinpic.com", "douyinstatic.com", "iesdouyin.com", "douyin.com", "bytecdn.cn", "byteimg.com", "pstatp.com", "ibyteimg.com", "amemv.com", "ixigua.com", "bytedance.com", legacyPlaybackHost}

const mobileUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"

func allowedURL(raw string, domains []string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || (u.Port() != "" && ((u.Scheme == "https" && u.Port() != "443") || (u.Scheme == "http" && u.Port() != "80"))) {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	for _, d := range domains {
		if d == legacyPlaybackHost {
			// The share page also returns this API playback gateway. Admit only
			// its exact HTTPS video endpoint, not the entire snssdk.com network.
			if host == d && u.Scheme == "https" && u.Path == "/aweme/v1/play/" {
				return true
			}
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func normalizeMediaURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if strings.EqualFold(u.Hostname(), legacyPlaybackHost) && (u.Scheme == "http" || u.Scheme == "https") {
		// The mobile share response calls the watermarked playback alias. The
		// ordinary play endpoint accepts the same video_id and returns the stream.
		if u.Path == "/aweme/v1/playwm/" {
			u.Path = "/aweme/v1/play/"
			u.RawPath = ""
		}
		if u.Scheme == "http" && (u.Port() == "" || u.Port() == "80") {
			u.Scheme = "https"
			u.Host = u.Hostname()
		}
		raw = u.String()
	}
	if !allowedURL(raw, mediaDomains) {
		return ""
	}
	return raw
}

func newHTTPClient(domains []string, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, MaxIdleConns: 32, IdleConnTimeout: 60 * time.Second, DisableCompression: true}
	return &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 || !allowedURL(req.URL.String(), domains) {
			return fmt.Errorf("重定向地址不在允许的抖音域名中")
		}
		// A Douyin session must never be forwarded to a media CDN or a sibling origin.
		if req.URL.Scheme != "https" || (len(via) > 0 && !strings.EqualFold(req.URL.Host, via[0].URL.Host)) {
			req.Header.Del("Cookie")
			req.Header.Del("uifid")
			req.Header.Del("x-secsdk-web-signature")
			req.Header.Del("x-secsdk-web-expire")
		}
		return nil
	}}
}

func request(ctx context.Context, client *http.Client, raw, cookie string, domains []string, extra ...http.Header) (*http.Response, error) {
	return requestMethod(ctx, client, http.MethodGet, raw, cookie, domains, extra...)
}
func requestMethod(ctx context.Context, client *http.Client, method, raw, cookie string, domains []string, extra ...http.Header) (*http.Response, error) {
	if !allowedURL(raw, domains) {
		return nil, fmt.Errorf("不支持的抖音地址")
	}
	req, err := http.NewRequestWithContext(ctx, method, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", mobileUA)
	req.Header.Set("Referer", "https://www.douyin.com/")
	for _, headers := range extra {
		for k, values := range headers {
			req.Header.Del(k)
			for _, v := range values {
				req.Header.Add(k, v)
			}
		}
	}
	if cookie != "" && allowedURL(raw, pageDomains) && req.URL.Scheme == "https" {
		req.Header.Set("Cookie", cookie)
	}
	l := &upstreamLog{started: time.Now(), request: req}
	req = req.WithContext(context.WithValue(req.Context(), upstreamLogKey{}, l))
	resp, err := client.Do(req)
	if err != nil {
		l.failure("request", resp, nil, err)
	}
	return resp, err
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fixtureID = "7000000000000000001"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(r *http.Request, kind string, body []byte) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}
}
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../../tests/fixtures/douyin", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(data, []byte("example-cdn.invalid"), []byte("douyinvod.com"))
}
func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestSharePageFormatsAndContentID(t *testing.T) {
	payload := fixture(t, "video_normal")
	for name, body := range map[string][]byte{
		"router": []byte(`<script>window._ROUTER_DATA = {"loaderData":{"video_(id)/page":` + string(payload) + `}};</script>`),
		"render": []byte(`<script id="RENDER_DATA" type="application/json">` + url.PathEscape(string(payload)) + `</script>`),
		"json":   payload,
	} {
		t.Run(name, func(t *testing.T) {
			v, err := parsePage(body, fixtureID)
			if err != nil {
				t.Fatal(err)
			}
			if v.ID != fixtureID || v.Author != "Synthetic Creator" || v.Duration != 15320 || len(v.MediaURLs) != 8 {
				t.Fatalf("wrong video: %+v", v)
			}
		})
	}
	if _, err := parsePage(payload, "7000000000000000999"); err == nil {
		t.Fatal("must not return another video's data")
	}
	for _, raw := range []string{"https://www.douyin.com/video/" + fixtureID, "https://www.iesdouyin.com/share/video/" + fixtureID + "/", "https://www.douyin.com/?modal_id=" + fixtureID} {
		if contentID(raw) != fixtureID {
			t.Fatal(raw)
		}
	}
	link, err := extractLink("3.26 复制打开抖音 https://v.douyin.com/abcDEF/，美好生活")
	if err != nil || link != "https://v.douyin.com/abcDEF/" {
		t.Fatalf("share text: %s %v", link, err)
	}
}
func TestRejectUnsupportedAndUnavailableContent(t *testing.T) {
	for _, raw := range []string{"https://douyin.com.evil.test/video/1", "https://evildouyin.com/video/1", "https://127.0.0.1/video/1", "file:///etc/passwd", "https://user:password@douyin.com/video/1", "https://douyin.com:8080/video/1"} {
		if _, err := extractLink(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, name := range []string{"risk_control_empty", "risk_control_captcha"} {
		data := fixture(t, name)
		var payload map[string]any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		_ = dec.Decode(&payload)
		id := fixtureID
		if detail := object(payload["aweme_detail"]); detail != nil {
			id = textValue(detail["aweme_id"])
		}
		if _, err := parsePage(data, id); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
}

func TestMobileSharePlaybackGateway(t *testing.T) {
	// Shape observed on the reported mobile share page. Synthetic identifiers
	// keep account data and expiring signed media URLs out of the fixture.
	body := []byte(`<script>window._ROUTER_DATA={"loaderData":{"video_(id)/page":{"videoInfoRes":{"item_list":[{"aweme_id":"` + fixtureID + `","desc":"分享页播放入口","author":{"nickname":"测试作者"},"video":{"duration":15000,"width":720,"height":1280,"play_addr":{"uri":"fixture-video-id","url_list":["https://aweme.snssdk.com/aweme/v1/playwm/?video_id=fixture-video-id&ratio=720p&line=0"]}}}]}}}};</script>`)
	v, err := parsePage(body, fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.MediaURLs) != 1 {
		t.Fatal("mobile share stream was filtered")
	}
	u, err := url.Parse(v.MediaURLs[0])
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Hostname() != legacyPlaybackHost || u.Path != "/aweme/v1/play/" || u.Query().Get("video_id") != "fixture-video-id" || u.Query().Get("ratio") != "720p" {
		t.Fatal("playback gateway normalization failed")
	}
	for _, raw := range []string{
		"https://aweme.snssdk.com/private/", "https://evil.aweme.snssdk.com/aweme/v1/play/", "https://aweme.snssdk.com.evil.test/aweme/v1/play/",
		"https://aweme.snssdk.com:8080/aweme/v1/play/", "https://user:password@aweme.snssdk.com/aweme/v1/play/", "ftp://aweme.snssdk.com/aweme/v1/playwm/",
	} {
		if normalizeMediaURL(raw) != "" {
			t.Fatalf("accepted unapproved playback URL: %s", raw)
		}
	}
	if got := normalizeMediaURL("http://aweme.snssdk.com/aweme/v1/playwm/?video_id=fixture"); got != "https://aweme.snssdk.com/aweme/v1/play/?video_id=fixture" {
		t.Fatal("legacy HTTP was not upgraded", got)
	}
}

func TestPlaybackGatewayRedirectDoesNotSendCookie(t *testing.T) {
	client := newHTTPClient(mediaDomains, time.Second)
	calls := 0
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Header.Get("Cookie") != "" {
			t.Error("session Cookie reached the playback gateway or CDN")
		}
		if calls == 1 {
			resp := response(req, "text/html", nil)
			resp.StatusCode = 302
			resp.Header.Set("Location", "https://v3.douyinvod.com/fixture.mp4")
			return resp, nil
		}
		return response(req, "video/mp4", []byte("fixture-video-bytes")), nil
	})
	resp, err := request(context.Background(), client, "https://aweme.snssdk.com/aweme/v1/play/?video_id=fixture", "sessionid=private", mediaDomains)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if calls != 2 || resp.Request.URL.Hostname() != "v3.douyinvod.com" || resp.StatusCode != 200 {
		t.Fatal("playback gateway did not reach the allowed CDN")
	}
}
func TestNetworkBoundary(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.1.1", "::1", "::ffff:192.168.1.1", "2001:db8::1", "198.18.1.1", "224.0.0.1", "0.0.0.0", "240.1.1.1"} {
		if publicIP(net.ParseIP(raw)) {
			t.Fatal("accepted private/reserved IP", raw)
		}
	}
	if !publicIP(net.ParseIP("8.8.8.8")) || !publicIP(net.ParseIP("2606:4700:4700::1111")) {
		t.Fatal("public IP refused")
	}
	client := newHTTPClient(pageDomains, time.Second)
	for _, raw := range []string{"http://127.0.0.1/", "https://evil.test/", "file:///etc/passwd"} {
		u, _ := url.Parse(raw)
		if err := client.CheckRedirect(&http.Request{URL: u}, []*http.Request{{}}); err == nil {
			t.Fatal("accepted redirect", raw)
		}
	}
	if err := ValidateListen("0.0.0.0:8080", ""); err == nil {
		t.Fatal("remote bind without auth")
	}
	if err := ValidateListen("0.0.0.0:8080", "test-token"); err != nil {
		t.Fatal(err)
	}
}

func TestShortLinkExpansionChecksEveryHop(t *testing.T) {
	for _, test := range []struct {
		name, location string
		valid          bool
	}{
		{"share target", "https://www.iesdouyin.com/share/video/" + fixtureID + "/", true},
		{"private target", "http://127.0.0.1/secret", false},
		{"off-platform target", "https://douyin.com.evil.test/video/" + fixtureID, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := NewResolver(newStore(t))
			calls := 0
			r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					resp := response(req, "text/html", nil)
					resp.StatusCode = 302
					resp.Header.Set("Location", test.location)
					return resp, nil
				}
				if !test.valid {
					t.Error("unsafe redirect was fetched")
				}
				return response(req, "application/json", fixture(t, "video_normal")), nil
			})
			v, err := r.Resolve(context.Background(), "复制打开抖音 https://v.douyin.com/fixture/，分享生活")
			if test.valid {
				if err != nil || v.ID != fixtureID || calls != 2 {
					t.Fatalf("expansion: %+v, calls=%d, err=%v", v, calls, err)
				}
			} else if err == nil || calls != 1 {
				t.Fatal("redirect boundary failed", calls, err)
			}
		})
	}
}
func TestSQLitePersistenceAndAttemptIsolation(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, err := parsePage(fixture(t, "video_normal"), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.Add(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add(v); err == nil {
		t.Fatal("duplicate video was inserted")
	}
	claimed, err := s.Claim()
	if err != nil || claimed.ID != task.ID {
		t.Fatal(err)
	}
	if err = s.SetCookie("sessionid=fixture-secret"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restored, err := s.Task(task.ID)
	if err != nil || restored.Status != "queued" || len(restored.MediaURLs) != 0 {
		t.Fatalf("restart: %+v %v", restored, err)
	}
	newAttempt, err := s.Claim()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(claimed, "failed", "stale worker"); err != nil {
		t.Fatal(err)
	}
	restored, _ = s.Task(task.ID)
	if restored.Status != "downloading" || restored.Attempt != newAttempt.Attempt {
		t.Fatal("stale worker changed new attempt")
	}
	cookie, err := s.Cookie()
	if err != nil || cookie != "sessionid=fixture-secret" {
		t.Fatal("settings did not persist")
	}
}

func call(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, "http://localhost"+path, reader)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
func waitTask(t *testing.T, s *Store, id, status string) Task {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		task, err := s.Task(id)
		if err == nil && task.Status == status {
			return task
		}
		time.Sleep(15 * time.Millisecond)
	}
	task, _ := s.Task(id)
	t.Fatalf("waiting for %s, got %+v", status, task)
	return Task{}
}
func TestAPIToQueueToDiskAndExport(t *testing.T) {
	s, err := NewServer(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	payload := fixture(t, "video_normal")
	var fail atomic.Bool
	s.resolver.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, "text/html", []byte(`<script>window._ROUTER_DATA = `+string(payload)+`;</script>`)), nil
	})
	video := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, bytes.Repeat([]byte{1}, 8192)...)
	s.queue.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if fail.Load() {
			resp := response(r, "text/html", []byte("login"))
			return resp, nil
		}
		if strings.Contains(r.URL.Path, "cover") {
			return response(r, "image/jpeg", []byte("synthetic-cover")), nil
		}
		return response(r, "video/mp4", video), nil
	})
	h := s.Handler()
	w := call(t, h, "POST", "/api/parse", map[string]string{"text": "https://www.douyin.com/video/" + fixtureID}, nil)
	if w.Code != 200 {
		t.Fatalf("parse: %d %s", w.Code, w.Body.String())
	}
	var parsed struct {
		PreviewID string `json:"preview_id"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	w = call(t, h, "POST", "/api/tasks", map[string]string{"preview_id": parsed.PreviewID}, nil)
	if w.Code != 201 {
		t.Fatalf("add: %d %s", w.Code, w.Body.String())
	}
	var task Task
	_ = json.Unmarshal(w.Body.Bytes(), &task)
	completed := waitTask(t, s.store, task.ID, "completed")
	if completed.Downloaded != int64(len(video)+len("synthetic-cover")) {
		t.Fatal("wrong byte count", completed.Downloaded)
	}
	w = call(t, h, "GET", "/api/tasks/"+task.ID+"/file?download=1", nil, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), video) || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("export failed")
	}
	w = call(t, h, "GET", "/api/tasks/"+task.ID+"/file", nil, map[string]string{"Range": "bytes=0-11"})
	if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), video[:12]) {
		t.Fatal("playback range not supported")
	}
	w = call(t, h, "PATCH", "/api/tasks/"+task.ID+"/favorite", map[string]bool{"favorite": true}, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = call(t, h, "GET", "/api/tasks", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "media_urls") || !strings.Contains(w.Body.String(), `"favorite":true`) {
		t.Fatal("list leaked source or lost favorite", w.Body.String())
	}
	w = call(t, h, "POST", "/api/tasks", map[string]string{"preview_id": parsed.PreviewID}, nil)
	if w.Code != 409 {
		t.Fatal("duplicate accepted")
	}
	w = call(t, h, "DELETE", "/api/tasks/"+task.ID, nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err = os.Stat(s.queue.file(task.ID)); !os.IsNotExist(err) {
		t.Fatal("deleted video still exists")
	}
	// A valid HTTP response containing HTML must become a visible failed task.
	fail.Store(true)
	w = call(t, h, "POST", "/api/tasks", map[string]string{"preview_id": parsed.PreviewID}, nil)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &task)
	failed := waitTask(t, s.store, task.ID, "failed")
	if !strings.Contains(failed.Error, "非视频") {
		t.Fatal(failed.Error)
	}
	fail.Store(false)
	w = call(t, h, "POST", "/api/tasks/"+task.ID+"/retry", nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	waitTask(t, s.store, task.ID, "completed")
}

func TestCancellationAndShutdownRecovery(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	resolver := NewResolver(s)
	q := &Queue{store: s, resolver: resolver, dir: dir, active: make(map[string]activeJob)}
	started := make(chan struct{})
	q.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	v := Video{ID: fixtureID, Title: "fixture", SourceURL: "https://www.douyin.com/video/" + fixtureID, MediaURLs: []string{"https://v.douyinvod.com/file.mp4"}}
	task, err := s.Add(v)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { q.run(context.Background(), claimed); close(done) }()
	<-started
	if err = q.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	<-done
	canceled, _ := s.Task(task.ID)
	if canceled.Status != "canceled" {
		t.Fatal("worker overwrote cancellation", canceled.Status)
	}
	if err = q.Retry(task.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.Claim()
	if err != nil {
		t.Fatal(err)
	}
	// Force a service shutdown during parsing after retry cleared the old URLs.
	started = make(chan struct{})
	resolver.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { q.run(ctx, claimed); close(done) }()
	<-started
	cancel()
	<-done
	restored, _ := s.Task(task.ID)
	if restored.Status != "queued" {
		t.Fatal("shutdown lost pending task", restored.Status)
	}
}

func TestAuthCookieAndRequestGuards(t *testing.T) {
	s, err := NewServer(t.TempDir(), "secret-fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := s.Handler()
	if w := call(t, h, "GET", "/api/tasks", nil, nil); w.Code != 401 {
		t.Fatal("anonymous access accepted")
	}
	w := call(t, h, "POST", "/api/session", map[string]string{"token": "secret-fixture-token"}, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("weak session cookie")
	}
	headers := map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value}
	w = call(t, h, "PUT", "/api/settings", map[string]string{"cookie": "sessionid=do-not-echo"}, headers)
	if w.Code != 200 || strings.Contains(w.Body.String(), "do-not-echo") {
		t.Fatal("Cookie leaked")
	}
	headers["Origin"] = "https://evil.test"
	if w = call(t, h, "PUT", "/api/settings", map[string]string{"cookie": ""}, headers); w.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	delete(headers, "Origin")
	if w = call(t, h, "POST", "/api/tasks", map[string]any{"url": "http://127.0.0.1/secret"}, headers); w.Code != 400 {
		t.Fatal("arbitrary media injection accepted")
	}
	if w = call(t, h, "PUT", "/api/settings", map[string]string{"cookie": "sessionid=abc\r\nInjected: yes"}, headers); w.Code != 400 {
		t.Fatal("header injection accepted")
	}
	if w = call(t, h, "POST", "/api/parse", map[string]string{"text": "https://evil.test/file"}, headers); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(t, h, "GET", "/api/nonexistent", nil, headers); w.Code != 404 || !strings.Contains(w.Body.String(), "接口不存在") {
		t.Fatal("API fallback failed")
	}
	// Cookie requests to another origin are removed on redirect.
	client := newHTTPClient(pageDomains, time.Second)
	origin, _ := http.NewRequest("GET", "https://v.douyin.com/a", nil)
	redirect, _ := http.NewRequest("GET", "https://www.iesdouyin.com/share/video/1", nil)
	redirect.Header.Set("Cookie", "sessionid=secret")
	if err = client.CheckRedirect(redirect, []*http.Request{origin}); err != nil || redirect.Header.Get("Cookie") != "" {
		t.Fatal("redirect leaked Cookie")
	}
}

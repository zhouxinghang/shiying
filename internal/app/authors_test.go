package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
)

const fixtureAuthor = "MS4wLjABAAAA_SYNTHETIC_DOUYIN_AUTHOR_0001"

func fixtureRoot(t *testing.T, name string) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(fixture(t, name), &root); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestAuthorReferenceParityAndSignedPagination(t *testing.T) {
	raw, err := os.ReadFile("testdata/reference_parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Authors struct {
			Queries map[string]string
			Profile map[string]any
			Page    struct {
				IDs     []string
				Cursor  string
				HasMore bool `json:"has_more"`
			}
		}
	}
	if err = json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	author, err := parseAuthorProfile(fixtureRoot(t, "user_profile"), fixtureAuthor)
	if err != nil {
		t.Fatal(err)
	}
	shape := jsonShape(t, author).(map[string]any)
	for key, want := range golden.Authors.Profile {
		if !reflect.DeepEqual(shape[key], want) {
			t.Errorf("profile field %s differs", key)
		}
	}
	page, err := parseAuthorPosts(fixtureRoot(t, "user_posts_page1"))
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, c := range page.Items {
		ids = append(ids, c.ID)
	}
	if !reflect.DeepEqual(ids, golden.Authors.Page.IDs) || page.Cursor != golden.Authors.Page.Cursor || page.HasMore != golden.Authors.Page.HasMore {
		t.Fatal("reference author page differs")
	}
	store := newStore(t)
	if err = store.SetCookie("msToken=fixture-token; UIFID_TEMP=synthetic-visitor"); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(store)
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		name := "profile"
		fixtureName := "user_profile"
		if strings.HasSuffix(req.URL.Path, "/post/") {
			name = "posts"
			fixtureName = "user_posts_page1"
			if req.URL.Query().Get("max_cursor") != "0" {
				name = "next"
			}
		}
		covered := strings.Split(req.URL.RawQuery, "&a_bogus=")[0]
		if covered != golden.Authors.Queries[name] {
			t.Errorf("%s ordered query differs from reference", name)
		}
		if req.URL.Query().Get("a_bogus") == "" || req.Header.Get("Cookie") == "" || req.Header.Get("x-secsdk-web-signature") == "" || req.Header.Get("User-Agent") != desktopUA {
			t.Error("author signing identity incomplete")
		}
		root := fixtureRoot(t, fixtureName)
		if name == "next" {
			root["has_more"] = 0
		}
		body, _ := json.Marshal(root)
		return response(req, "application/json", body), nil
	})
	if _, err = r.ResolveAuthor(context.Background(), "https://www.douyin.com/user/"+fixtureAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err = r.AuthorPosts(context.Background(), fixtureAuthor, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = r.AuthorPosts(context.Background(), fixtureAuthor, "1716300000000"); err != nil {
		t.Fatal(err)
	}
}
func TestAuthorPagingGuardsAndNoCookie(t *testing.T) {
	r := NewResolver(newStore(t))
	if _, err := r.AuthorPosts(context.Background(), fixtureAuthor, ""); err == nil {
		t.Fatal("missing Cookie was not explained")
	}
	for _, uid := range []string{"12345", "MS4/../../etc", "MS4evil?x=y", fixtureAuthor + "@evil.invalid"} {
		if _, err := r.AuthorPosts(context.Background(), uid, ""); err == nil {
			t.Fatal("invalid author accepted")
		}
	}
	for _, cursor := range []string{"-1", "NaN", "0&count=100", "999999999999999999999"} {
		if _, err := r.AuthorPosts(context.Background(), fixtureAuthor, cursor); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
	for _, change := range []func(map[string]any){func(root map[string]any) { delete(root, "aweme_list") }, func(root map[string]any) { delete(root, "has_more") }, func(root map[string]any) { root["max_cursor"] = 0 }, func(root map[string]any) { root["aweme_list"] = []any{nil} }} {
		root := fixtureRoot(t, "user_posts_page1")
		change(root)
		_, err := parseAuthorPosts(root)
		var classified *ParseError
		if !errors.As(err, &classified) || classified.Code != "UPSTREAM_CHANGED" {
			t.Fatal("malformed page was silently accepted", err)
		}
	}
	root := fixtureRoot(t, "user_posts_page1")
	root["aweme_list"] = []any{}
	root["has_more"] = 0
	page, err := parseAuthorPosts(root)
	if err != nil || len(page.Items) != 0 || page.HasMore || page.Cursor != "" {
		t.Fatal("empty final page refused", err)
	}
	for _, raw := range []string{"https://douyin.com.evil.invalid/user/" + fixtureAuthor, "https://user:password@www.douyin.com/user/" + fixtureAuthor, "https://www.douyin.com:999/user/" + fixtureAuthor} {
		if authorUID(raw) != "" {
			t.Fatal("unsafe author URL accepted")
		}
	}
}
func TestAuthorCatalogBatchIdempotencyAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.SetCookie("msToken=fixture-token"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.resolver.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		name := "user_posts_page1"
		if strings.Contains(req.URL.Path, "profile/other") {
			name = "user_profile"
		}
		return response(req, "application/json", fixture(t, name)), nil
	})
	s.queue.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "cover") || strings.Contains(req.URL.Path, "synthetic-album") {
			return response(req, "image/jpeg", []byte("synthetic-picture")), nil
		}
		return response(req, "video/mp4", []byte("synthetic-video")), nil
	})
	h := s.Handler()
	w := call(t, h, "POST", "/api/authors/resolve", map[string]string{"text": "https://www.douyin.com/user/" + fixtureAuthor}, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "douyinvod.com") {
		t.Fatal("author profile failed or leaked CDN", w.Body.String())
	}
	w = call(t, h, "GET", "/api/authors/"+fixtureAuthor+"/posts", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "douyinvod.com") {
		t.Fatal("author posts failed or leaked CDN", w.Body.String())
	}
	var page struct{ Items []AuthorPost }
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Items) != 2 {
		t.Fatal("wrong page size")
	}
	ids := []string{page.Items[0].PreviewID, page.Items[1].PreviewID}
	first := ids[0]
	// Page previews survive normal memory preview eviction and retain signed
	// manifests privately; no production account data is part of these fixtures.
	if _, ok := s.getPreview(first); !ok {
		t.Fatal("catalog preview unavailable")
	}
	if c, e := s.store.CatalogContent(first); e != nil || len(buildPlan(c, maxVideoBytes, true).Items) == 0 {
		t.Fatal("catalog lost manifest")
	}
	w = call(t, h, "GET", "/api/previews/"+first+"/cover", nil, nil)
	if w.Code != 200 {
		t.Fatal("catalog cover unavailable", w.Body.String())
	}
	private, err := parsePage(fixture(t, "video_private"), "7000000000000000004")
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := s.store.SaveCatalog(fixtureAuthor, private)
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, privateKey, "unknown-preview", first)
	w = call(t, h, "POST", "/api/tasks/batch", map[string]any{"preview_ids": ids}, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result struct{ Results []batchResult }
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	for _, result := range result.Results {
		if result.State == "added" {
			waitTask(t, s.store, result.TaskID, "completed")
		}
	}
	counts := map[string]int{}
	for _, r := range result.Results {
		counts[r.State]++
	}
	if counts["added"] != 2 || counts["failed"] != 2 || len(result.Results) != 4 {
		t.Fatal("batch lost partial results or duplicate", result)
	}
	w = call(t, h, "POST", "/api/tasks/batch", map[string]any{"preview_ids": ids[:2]}, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	for _, r := range result.Results {
		if r.State != "existing" {
			t.Fatal("duplicate batch created new tasks")
		}
	}
	if tasks, e := s.store.List(); e != nil || len(tasks) != 2 {
		t.Fatal("batch task count differs", e)
	}
	for _, list := range [][]string{nil, make([]string, 101)} {
		w = call(t, h, "POST", "/api/tasks/batch", map[string]any{"preview_ids": list}, nil)
		if w.Code != 400 {
			t.Fatal("invalid batch size accepted")
		}
	}
	// Stable preview keys remain valid when an author page is refreshed.
	w = call(t, h, "GET", "/api/authors/"+fixtureAuthor+"/posts", nil, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if page.Items[0].PreviewID != first {
		t.Fatal("page refresh invalidated selections")
	}
	s.Close()
	reopened, err := NewServer(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if c, ok := reopened.getPreview(first); !ok || c.ID != fixtureID {
		t.Fatal("service restart lost catalog selection")
	}
	if profile, e := reopened.store.Author(fixtureAuthor); e != nil || profile.AvatarImage == nil {
		t.Fatal("author profile did not persist")
	}
	if calls != 3 {
		t.Fatal("batch unexpectedly reparsed recent catalog items", calls)
	}
}
func TestAuthorRepeatedCursorStops(t *testing.T) {
	s := newStore(t)
	_ = s.SetCookie("msToken=fixture-token")
	r := NewResolver(s)
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return response(req, "application/json", fixture(t, "user_posts_page1")), nil
	})
	_, err := r.AuthorPosts(context.Background(), fixtureAuthor, "1716300000000")
	if err == nil {
		t.Fatal("repeated cursor allowed")
	}
}
func TestAuthorFromWorkLink(t *testing.T) {
	s := newStore(t)
	_ = s.SetCookie("msToken=fixture-token")
	r := NewResolver(s)
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		name := "video_normal"
		if strings.Contains(req.URL.Path, "profile/other") {
			name = "user_profile"
		}
		return response(req, "application/json", fixture(t, name)), nil
	})
	profile, err := r.ResolveAuthor(context.Background(), "3.14 复制 https://www.douyin.com/video/"+fixtureID+"，查看作品")
	if err != nil || profile.UID != fixtureAuthor {
		t.Fatal("work-to-author resolution failed", err)
	}
	if _, err = url.Parse(profile.WebURL); err != nil {
		t.Fatal(err)
	}
}

func TestShortProfileLinkNeverFetchesProfileHTML(t *testing.T) {
	s := newStore(t)
	_ = s.SetCookie("msToken=fixture-token; sessionid=synthetic-session")
	r := NewResolver(s)
	calls := 0
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Hostname() == "v.douyin.com" {
			if req.Header.Get("Cookie") != "" {
				t.Error("short link expansion received session")
			}
			resp := response(req, "text/html", nil)
			resp.StatusCode = 302
			resp.Header.Set("Location", "https://www.douyin.com/user/"+fixtureAuthor)
			return resp, nil
		}
		if strings.Contains(req.URL.Path, "/user/profile/other/") {
			return response(req, "application/json", fixture(t, "user_profile")), nil
		}
		t.Error("expanded profile URL should go directly to the signed profile API")
		return response(req, "text/html", nil), nil
	})
	profile, err := r.ResolveAuthor(context.Background(), "https://v.douyin.com/synthetic-profile/")
	if err != nil || profile.UID != fixtureAuthor || calls != 2 {
		t.Fatal("profile expansion differs", calls, err)
	}
}

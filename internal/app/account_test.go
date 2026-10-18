package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const fixtureAccount = "MS4wLjABAAAA_SYNTHETIC_LOGIN_OWNER_0001"
const fixtureAccountNumeric = "1000000000000000999"
const fixtureLoginCookie = "sessionid=synthetic-login-a; msToken=fixture-token; UIFID_TEMP=fixture-visitor"

func selfFixture(t *testing.T) map[string]any {
	root := fixtureRoot(t, "user_profile")
	user := object(root["user"])
	user["uid"] = fixtureAccountNumeric
	user["sec_uid"] = fixtureAccount
	user["nickname"] = "测试登录账号"
	user["favoriting_count"] = 3
	user["bind_phone"] = "private-field-must-not-escape"
	root["user_collect_count"] = 3
	return root
}
func personalFixture(t *testing.T, kind, cursor string) map[string]any {
	root := fixtureRoot(t, "user_posts_page1")
	root["uid"] = fixtureAccountNumeric
	root["sec_uid"] = fixtureAccount
	if kind == "bookmarks" {
		root["cursor"] = 20
		root["max_cursor"] = "999999"
		root["invalid_item_count"] = 1
	}
	if cursor != "0" {
		root["has_more"] = 0
		root["aweme_list"] = []any{object(fixtureRoot(t, "video_long_desc")["aweme_detail"])}
		root["cursor"] = 0
	}
	return root
}
func accountTransport(t *testing.T) roundTripFunc {
	raw, err := os.ReadFile("testdata/reference_parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Queries map[string]string `json:"personal_queries"`
	}
	if err = json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	return func(req *http.Request) (*http.Response, error) {
		t.Helper()
		var root map[string]any
		queryName := "self"
		if req.Header.Get("Cookie") != fixtureLoginCookie {
			t.Error("personal API did not use the pinned cookie")
		}
		switch req.URL.Path {
		case "/aweme/v1/web/user/profile/self/":
			if req.URL.Query().Get("sec_user_id") != "" || req.URL.Query().Get("user_id") != "" {
				t.Error("self identity was supplied by caller")
			}
			root = selfFixture(t)
		case "/aweme/v1/web/aweme/favorite/":
			queryName = "likes"
			if req.URL.Query().Get("max_cursor") != "0" {
				queryName = "likes_next"
			}
			if req.Method != "GET" || req.URL.Query().Get("sec_user_id") != fixtureAccount || req.URL.Query().Get("count") != "20" {
				t.Error("likes must address Cookie owner, not a work creator")
			}
			root = personalFixture(t, "likes", req.URL.Query().Get("max_cursor"))
		case "/aweme/v1/web/aweme/listcollection/":
			queryName = "bookmarks"
			if req.URL.Query().Get("cursor") != "0" {
				queryName = "bookmarks_next"
			}
			if req.Method != "POST" || req.URL.Query().Get("sec_user_id") != "" || req.URL.Query().Get("max_cursor") != "" {
				t.Error("bookmark method or owner semantics differ")
			}
			root = personalFixture(t, "bookmarks", req.URL.Query().Get("cursor"))
		default:
			t.Errorf("unexpected endpoint %s", req.URL.Path)
			root = map[string]any{"status_code": 8}
		}
		if covered := strings.Split(req.URL.RawQuery, "&a_bogus=")[0]; covered != golden.Queries[queryName] {
			t.Errorf("%s ordered personal query differs from reference", queryName)
		}
		if req.URL.Query().Get("a_bogus") == "" || req.Header.Get("x-secsdk-web-signature") == "" || req.Header.Get("User-Agent") != desktopUA {
			t.Error("personal API signature or identity incomplete")
		}
		raw, _ := json.Marshal(root)
		return response(req, "application/json", raw), nil
	}
}
func TestCookieAccountFeedsPaginationAndBatchDownload(t *testing.T) {
	s, err := NewServer(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.store.SetCookie(fixtureLoginCookie); err != nil {
		t.Fatal(err)
	}
	s.resolver.client.Transport = accountTransport(t)
	s.queue.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Cookie") != "" {
			t.Error("media request leaked Cookie")
		}
		kind := "video/mp4"
		if strings.Contains(req.URL.Path, "cover") || strings.Contains(req.URL.Path, "synthetic-album") {
			kind = "image/jpeg"
		}
		return response(req, kind, []byte("synthetic-media")), nil
	})
	h := s.Handler()
	w := call(t, h, "GET", "/api/account", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-field") || strings.Contains(w.Body.String(), "sessionid") || strings.Contains(w.Body.String(), "douyinvod.com") {
		t.Fatal("account not verified or response leaked private fields", w.Code)
	}
	var account accountSession
	if err = json.Unmarshal(w.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	if account.Profile.UID != fixtureAccount || account.Scope == "" || account.LikesCount == nil || *account.LikesCount != 3 {
		t.Fatal("Cookie owner not identified")
	}
	var keys []string
	for _, kind := range []string{"likes", "bookmarks"} {
		w = call(t, h, "GET", "/api/account/"+kind+"?scope="+account.Scope, nil, nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), "douyinvod.com") {
			t.Fatal("personal feed failed or leaked sources", w.Code, w.Body.String())
		}
		var page struct {
			Items   []AuthorPost
			Cursor  string
			HasMore bool `json:"has_more"`
			Notice  string
			Scope   string `json:"account_scope"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		if len(page.Items) != 2 || !page.HasMore || page.Scope != account.Scope {
			t.Fatal("wrong personal page")
		}
		if kind == "bookmarks" && (page.Cursor != "20" || page.Notice == "") {
			t.Fatal("bookmark cursor or invalid-item notice missing")
		}
		if kind == "likes" {
			for _, item := range page.Items {
				if item.Content.AuthorUID != fixtureAuthor {
					t.Fatal("work author was replaced by login owner")
				}
				keys = append(keys, item.PreviewID)
			}
		} else if page.Items[0].PreviewID != keys[0] {
			t.Fatal("same work got inconsistent keys across personal feeds")
		}
		if _, e := s.store.CatalogContent(page.Items[0].PreviewID); !errors.Is(e, sql.ErrNoRows) {
			t.Fatal("personal work was put into public author catalog")
		}
		w = call(t, h, "GET", "/api/account/"+kind+"?scope="+account.Scope+"&cursor="+page.Cursor, nil, nil)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		if page.HasMore || page.Cursor != "" {
			t.Fatal("personal final page not recognized")
		}
	}
	w = call(t, h, "POST", "/api/tasks/batch", map[string]any{"preview_ids": keys}, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var batch struct{ Results []batchResult }
	_ = json.Unmarshal(w.Body.Bytes(), &batch)
	for _, result := range batch.Results {
		if result.State != "added" {
			t.Fatal("personal batch not queued", result)
		}
		waitTask(t, s.store, result.TaskID, "completed")
	}
	w = call(t, h, "POST", "/api/tasks/batch", map[string]any{"preview_ids": keys}, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &batch)
	for _, result := range batch.Results {
		if result.State != "existing" {
			t.Fatal("personal batch was not deduplicated")
		}
	}
}
func TestCookieSwitchInvalidatesOnlyPersonalCache(t *testing.T) {
	s, err := NewServer(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.store.SetCookie(fixtureLoginCookie)
	_, scope, _ := s.store.Credentials()
	c, err := parsePage(fixture(t, "video_normal"), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := s.store.SavePersonal(scope, c)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := s.store.SaveCatalog(fixtureAuthor, c)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.store.SetCookie(fixtureLoginCookie)
	_, same, _ := s.store.Credentials()
	if same != scope {
		t.Fatal("saving identical Cookie needlessly invalidated current selections")
	}
	_ = s.store.SetCookie("sessionid=synthetic-login-b; msToken=fixture-token")
	_, next, _ := s.store.Credentials()
	if next == scope {
		t.Fatal("changed Cookie retained old scope")
	}
	if _, ok := s.getPreview(privateKey); ok {
		t.Fatal("old account preview survived Cookie change")
	}
	if _, ok := s.getPreview(publicKey); !ok {
		t.Fatal("Cookie change deleted public author previews")
	}
	if _, e := s.store.SavePersonal(scope, c); e == nil {
		t.Fatal("old in-flight request resurrected private cache")
	}
	h := s.Handler()
	for _, path := range []string{"/api/account/likes?scope=" + scope, "/api/account/bookmarks?scope=" + scope, "/api/account/avatar?scope=" + scope} {
		w := call(t, h, "GET", path, nil, nil)
		if w.Code != 422 || !strings.Contains(w.Body.String(), "ACCOUNT_CHANGED") {
			t.Fatal("stale scope was accepted", path, w.Code)
		}
	}
	w := call(t, h, "GET", "/api/previews/"+privateKey+"/cover", nil, nil)
	if w.Code != 404 {
		t.Fatal("old private preview was accessible")
	}
	w = call(t, h, "POST", "/api/tasks/batch", map[string]any{"preview_ids": []string{privateKey}}, nil)
	if !strings.Contains(w.Body.String(), `"state":"failed"`) {
		t.Fatal("old account selection was downloaded")
	}
	if tasks, e := s.store.List(); e != nil || len(tasks) != 0 {
		t.Fatal("stale account queued a task")
	}
	_ = s.store.SetCookie("")
	_, cleared, _ := s.store.Credentials()
	if cleared == next {
		t.Fatal("clearing Cookie retained scope")
	}
}
func TestPersonalAccountRejectsGuestsOtherOwnersAndUIDArguments(t *testing.T) {
	s, err := NewServer(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := s.Handler()
	for _, cookie := range []string{"", "msToken=synthetic-guest; UIFID_TEMP=synthetic-visitor"} {
		_ = s.store.SetCookie(cookie)
		w := call(t, h, "GET", "/api/account", nil, nil)
		if w.Code != 422 {
			t.Fatal("guest accepted as login")
		}
	}
	_ = s.store.SetCookie(fixtureLoginCookie)
	s.resolver.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		root := selfFixture(t)
		object(root["user"])["uid"] = "0"
		raw, _ := json.Marshal(root)
		return response(req, "application/json", raw), nil
	})
	if w := call(t, h, "GET", "/api/account", nil, nil); w.Code != 422 {
		t.Fatal("self response with no account UID accepted")
	}
	s.resolver.client.Transport = accountTransport(t)
	w := call(t, h, "GET", "/api/account", nil, nil)
	var account accountSession
	_ = json.Unmarshal(w.Body.Bytes(), &account)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = call(t, h, "GET", "/api/account?uid="+fixtureAuthor, nil, nil); w.Code != 400 {
		t.Fatal("self endpoint accepted a subject")
	}
	w = call(t, h, "GET", "/api/account/likes?scope="+account.Scope+"&sec_user_id="+fixtureAuthor, nil, nil)
	if w.Code != 422 {
		t.Fatal("personal likes accepted a stranger")
	}
	if w = call(t, h, "GET", "/api/account/bookmarks", nil, nil); w.Code != 400 {
		t.Fatal("personal endpoint accepted an unverified scope")
	}
	s.resolver.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		root := personalFixture(t, "likes", "0")
		root["sec_uid"] = fixtureAuthor
		raw, _ := json.Marshal(root)
		return response(req, "application/json", raw), nil
	})
	w = call(t, h, "GET", "/api/account/likes?scope="+account.Scope, nil, nil)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "ACCOUNT_MISMATCH") {
		t.Fatal("foreign owner's likes were presented as own")
	}
}
func TestCookieChangeDuringPersonalRequestCannotRepopulateOldList(t *testing.T) {
	s, err := NewServer(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.store.SetCookie(fixtureLoginCookie)
	s.resolver.client.Transport = accountTransport(t)
	h := s.Handler()
	w := call(t, h, "GET", "/api/account", nil, nil)
	var account accountSession
	_ = json.Unmarshal(w.Body.Bytes(), &account)
	started, release := make(chan struct{}), make(chan struct{})
	s.resolver.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-release
		raw, _ := json.Marshal(personalFixture(t, "likes", "0"))
		return response(req, "application/json", raw), nil
	})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(t, h, "GET", "/api/account/likes?scope="+account.Scope, nil, nil) }()
	<-started
	_ = s.store.SetCookie("sessionid=synthetic-login-b")
	close(release)
	w = <-done
	if w.Code != 422 || !strings.Contains(w.Body.String(), "ACCOUNT_CHANGED") || strings.Contains(w.Body.String(), "Sunrise") {
		t.Fatal("old account response leaked into new scope")
	}
	var n int
	if e := s.store.db.QueryRow("SELECT COUNT(*) FROM personal_catalog").Scan(&n); e != nil || n != 0 {
		t.Fatal("in-flight old request repopulated personal cache")
	}
}

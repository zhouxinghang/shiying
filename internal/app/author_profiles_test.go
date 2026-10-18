package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthorProfileCacheRefreshFallbackAndPrivacy(t *testing.T) {
	s, err := NewServer(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.store.SetCookie("msToken=synthetic-secret-token")
	var count atomic.Int32
	s.resolver.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		count.Add(1)
		if r.Method != "GET" || r.URL.Path != "/aweme/v1/web/user/profile/other/" || r.URL.Query().Get("sec_user_id") != fixtureAuthor {
			t.Error("profile fetched from incorrect endpoint or identity")
		}
		root := fixtureRoot(t, "user_profile")
		object(root["user"])["follower_count"] = 0
		raw, _ := json.Marshal(root)
		return response(r, "application/json", raw), nil
	})
	h := s.Handler()
	path := "/api/authors/" + fixtureAuthor + "/profile"
	var original cachedAuthorProfile
	for i := 0; i < 2; i++ {
		w := call(t, h, "GET", path, nil, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "douyinvod.com") || strings.Contains(w.Body.String(), "synthetic-secret-token") {
			t.Fatal("profile exposed private network data")
		}
		_ = json.Unmarshal(w.Body.Bytes(), &original)
		if original.Stale || original.FetchedAt == "" || original.Profile.FollowerCount == nil || *original.Profile.FollowerCount != 0 || !strings.HasPrefix(original.Profile.Avatar, "/api/authors/") {
			t.Fatal("missing profile metadata or genuine zero fans was lost")
		}
	}
	if count.Load() != 1 {
		t.Fatal("fresh profile fetched repeatedly")
	}
	// Explicit refresh bypasses the 24-hour cache.
	if w := call(t, h, "GET", path+"?refresh=1", nil, nil); w.Code != 200 || count.Load() != 2 {
		t.Fatal("explicit refresh did not run")
	}
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	_, _ = s.store.db.Exec("UPDATE authors SET updated_at=? WHERE uid=?", old, fixtureAuthor)
	s.resolver.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, "application/json", []byte(`{"status_code":8}`)), nil
	})
	w := call(t, h, "GET", path, nil, nil)
	var stale cachedAuthorProfile
	_ = json.Unmarshal(w.Body.Bytes(), &stale)
	if w.Code != 200 || !stale.Stale || stale.Warning == "" || stale.WarningCode != "UPSTREAM_RISK_CONTROL" || stale.FetchedAt != old || stale.Profile.FollowerCount == nil || *stale.Profile.FollowerCount != 0 {
		t.Fatal("failed update discarded or misrepresented cached metadata")
	}
	if cached, e := s.store.CachedAuthor(fixtureAuthor); e != nil || cached.FetchedAt != old {
		t.Fatal("failed refresh overwrote last successful metadata")
	}
	if w = call(t, h, "GET", "/api/authors/12345/profile", nil, nil); w.Code != 400 {
		t.Fatal("invalid UID accepted")
	}
}

func TestLibraryAuthorCacheContainsOnlySavedCreators(t *testing.T) {
	s, err := NewServer(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.queue.Close()
	profile, err := parseAuthorProfile(fixtureRoot(t, "user_profile"), fixtureAuthor)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.SaveAuthor(profile); err != nil {
		t.Fatal(err)
	}
	stranger := profile
	stranger.UID = fixtureAuthor + "_OTHER"
	stranger.Nickname = "not-a-local-creator"
	if err = s.store.SaveAuthor(stranger); err != nil {
		t.Fatal(err)
	}
	c, err := parsePage(fixture(t, "video_normal"), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.store.Add(c)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.store.db.Exec("UPDATE tasks SET status='completed' WHERE id=?", task.ID)
	w := call(t, s.Handler(), "GET", "/api/library/authors", nil, nil)
	var result struct{ Items []cachedAuthorProfile }
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || len(result.Items) != 1 || result.Items[0].Profile.UID != fixtureAuthor || strings.Contains(w.Body.String(), "not-a-local-creator") || strings.Contains(w.Body.String(), "douyinvod.com") {
		t.Fatal("cached author directory scope or URL privacy differs")
	}
	_, _ = s.store.db.Exec("UPDATE tasks SET status='failed' WHERE id=?", task.ID)
	w = call(t, s.Handler(), "GET", "/api/library/authors", nil, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Items) != 0 {
		t.Fatal("unsaved author exposed in library directory")
	}
}

func TestConcurrentProfileReadsCoalesceAndSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.store.SetCookie("msToken=synthetic-token")
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	s.resolver.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		body, _ := json.Marshal(fixtureRoot(t, "user_profile"))
		return response(r, "application/json", body), nil
	})
	h, path := s.Handler(), "/api/authors/"+fixtureAuthor+"/profile"
	done := make(chan *httptest.ResponseRecorder, 2)
	go func() { done <- call(t, h, "GET", path, nil, nil) }()
	<-started
	go func() { done <- call(t, h, "GET", path, nil, nil) }()
	close(release)
	for i := 0; i < 2; i++ {
		if w := <-done; w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent readers duplicated upstream work")
	}
	s.Close()
	restarted, err := NewServer(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.resolver.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Error("fresh persisted profile unnecessarily fetched after restart")
		return response(r, "application/json", []byte(`{}`)), nil
	})
	if w := call(t, restarted.Handler(), "GET", path, nil, nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

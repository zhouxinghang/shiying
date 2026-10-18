package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in fixture server for repeatable browser QA without a Douyin account.
// It only listens on loopback, creates a temporary database, and is never part of the binary.
func TestBrowserQA(t *testing.T) {
	if os.Getenv("SHIYING_UI_QA") != "1" {
		t.Skip("set SHIYING_UI_QA=1 to serve disposable fixtures at 127.0.0.1:8081")
	}
	s, err := NewServer(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.store.SetCookie("sessionid=synthetic-login-a; msToken=synthetic-token; UIFID_TEMP=synthetic-visitor"); err != nil {
		t.Fatal(err)
	}
	payload := fixture(t, "video_normal")
	album := fixture(t, "video_image_album")
	payload = bytes.ReplaceAll(payload, []byte("Synthetic Creator"), []byte("拾影测试作者"))
	video, err := os.ReadFile("testdata/sample.mp4")
	if err != nil {
		t.Fatal(err)
	}
	cover, err := os.ReadFile("testdata/cover.png")
	if err != nil {
		t.Fatal(err)
	}
	s.resolver.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/user/profile/self/") {
			root := selfFixture(t)
			object(root["user"])["nickname"] = "拾影测试账号"
			body, _ := json.Marshal(root)
			return response(r, "application/json", body), nil
		}
		if strings.HasSuffix(r.URL.Path, "/aweme/favorite/") || strings.HasSuffix(r.URL.Path, "/aweme/listcollection/") {
			kind, cursor := "likes", r.URL.Query().Get("max_cursor")
			if strings.HasSuffix(r.URL.Path, "/aweme/listcollection/") {
				kind, cursor = "bookmarks", r.URL.Query().Get("cursor")
			}
			root := personalFixture(t, kind, cursor)
			for i, raw := range root["aweme_list"].([]any) {
				item := object(raw)
				item["desc"] = "拾影测试视频 · 一段值得收藏的日常"
				if i == 1 {
					item["desc"] = "拾影测试图集 · 三张值得收藏的日常"
				}
				if cursor != "0" {
					item["desc"] = "拾影测试视频 · 跨页批量选择"
				}
			}
			body, _ := json.Marshal(root)
			return response(r, "application/json", body), nil
		}
		if strings.Contains(r.URL.Path, "/user/profile/other/") {
			root := fixtureRoot(t, "user_profile")
			user := object(root["user"])
			uid := r.URL.Query().Get("sec_user_id")
			user["sec_uid"] = uid
			user["nickname"] = "拾影测试作者"
			user["signature"] = "把日常里的光和风景，妥善收藏。\n视频与图集，记录每一个值得留下的片刻。"
			user["follower_count"] = 128600
			user["aweme_count"] = 4
			switch {
			case strings.HasSuffix(uid, "_B"):
				user["nickname"], user["signature"], user["follower_count"] = "慢慢记录", "慢一点，让生活有被看见的机会。", 25000
			case strings.HasSuffix(uid, "_C"):
				user["nickname"], user["signature"], user["follower_count"] = "同名作者", "第一位同名作者，喜欢记录日常。", 0
			case strings.HasSuffix(uid, "_D"):
				user["nickname"], user["signature"], user["follower_count"] = "同名作者", "另一位同名作者，用图片分享生活。", 90210
			}
			body, _ := json.Marshal(root)
			return response(r, "application/json", body), nil
		}
		if strings.HasSuffix(r.URL.Path, "/aweme/post/") {
			root := fixtureRoot(t, "user_posts_page1")
			if r.URL.Query().Get("max_cursor") != "0" {
				var extra map[string]any
				_ = json.Unmarshal(payload, &extra)
				last := object(extra["aweme_detail"])
				last["aweme_id"] = "7000000000000000005"
				last["desc"] = "拾影测试视频 · 跨页批量选择"
				root["aweme_list"] = []any{last}
				root["has_more"] = 0
			}
			body, _ := json.Marshal(root)
			return response(r, "application/json", body), nil
		}
		chosen := payload
		if contentID(r.URL.String()) == "7000000000000000002" {
			chosen = album
		}
		body := bytes.ReplaceAll(chosen, []byte(fixtureID), []byte(contentID(r.URL.String())))
		var item map[string]any
		if err := json.Unmarshal(body, &item); err != nil {
			return nil, err
		}
		if contentID(r.URL.String()) == "7000000000000000002" {
			object(item["aweme_detail"])["desc"] = "拾影测试图集 · 三张值得收藏的日常"
		} else {
			object(item["aweme_detail"])["desc"] = "拾影测试视频 · 一段值得收藏的日常"
		}
		body, _ = json.Marshal(item)
		if strings.Contains(r.URL.Path, "/aweme/detail/") {
			return response(r, "application/json", body), nil
		}
		return response(r, "text/html", []byte(`<script>window._ROUTER_DATA = `+string(body)+`;</script>`)), nil
	})
	s.queue.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "cover") || strings.Contains(r.URL.Path, "avatar") || strings.Contains(r.URL.Path, "synthetic-album") {
			return response(r, "image/png", cover), nil
		}
		return response(r, "video/mp4", video), nil
	})
	if os.Getenv("SHIYING_UI_QA_LIBRARY") == "1" {
		for i, item := range []struct{ author, uid, title, fixture string }{
			{"拾影测试作者", fixtureAuthor, "把今天的光，留给明天", "video_normal"},
			{"拾影测试作者", fixtureAuthor, "日常里的小小风景 · 三张图集", "video_image_album"},
			{"拾影测试作者", fixtureAuthor, "周末散步 · 好好感受生活", "video_normal"},
			{"慢慢记录", fixtureAuthor + "_B", "慢下来，看看窗外", "video_normal"},
			{"同名作者", fixtureAuthor + "_C", "这是来自第一位同名作者的作品", "video_normal"},
			{"同名作者", fixtureAuthor + "_D", "这是来自另一位同名作者的作品", "video_image_album"},
			{"旧收藏作者", "", "旧版下载 · 没有作者 ID 的收藏", "video_normal"},
		} {
			root := fixtureRoot(t, item.fixture)
			c, err := parsePage(fixture(t, item.fixture), textValue(object(root["aweme_detail"])["aweme_id"]))
			if err != nil {
				t.Fatal(err)
			}
			c.ID = fmt.Sprintf("700000000000000001%d", i)
			c.Author, c.AuthorUID, c.Title, c.Description = item.author, item.uid, item.title, item.title
			path := "video"
			if c.Kind == "image_album" {
				path = "note"
			}
			c.SourceURL = "https://www.douyin.com/" + path + "/" + c.ID
			task, err := s.store.Add(c)
			if err != nil {
				t.Fatal(err)
			}
			waitTask(t, s.store, task.ID, "completed")
			if _, err = s.store.db.Exec("UPDATE tasks SET favorite=?,created_at=? WHERE id=?", i < 3, fmt.Sprintf("2026-10-%02dT10:00:00Z", 17-i), task.ID); err != nil {
				t.Fatal(err)
			}
		}
		t.Log("seeded 7 disposable library works across 5 author identities, including duplicate nicknames and legacy records")
	}
	server := &http.Server{Addr: "127.0.0.1:8081", Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	t.Log("disposable UI QA server: http://127.0.0.1:8081; parse https://www.douyin.com/video/7000000000000000001")
	select {
	case <-ctx.Done():
	case err := <-done:
		if err != http.ErrServerClosed {
			t.Error(err)
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}

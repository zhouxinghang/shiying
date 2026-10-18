package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func jsonShape(t *testing.T, v any) any {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	var out any
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestReferenceParserAndPlanParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/reference_parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Sources     map[string]string `json:"sources"`
		DetailQuery string            `json:"detail_query"`
		Cases       []struct {
			Fixture string
			Error   string
			Content map[string]any
			Plan    Plan
		}
	}
	if err = json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	signed, _ := signedDetailURL(fixtureID, "msToken=fixture-token", 1789010742123)
	gotQuery := strings.Split(strings.SplitN(signed, "?", 2)[1], "&a_bogus=")[0]
	if gotQuery != golden.DetailQuery {
		t.Fatal("detail query ordering or profile differs from reference")
	}
	for name, want := range golden.Sources {
		b, e := os.ReadFile(filepath.Join("../../..", name))
		if e != nil {
			t.Fatal(e)
		}
		digest := sha256.Sum256(b)
		if hex.EncodeToString(digest[:]) != want {
			t.Fatalf("reference source %s changed; regenerate and review golden data", name)
		}
	}
	for _, tc := range golden.Cases {
		t.Run(tc.Fixture, func(t *testing.T) {
			id := fixtureID
			if tc.Content != nil {
				id = tc.Content["id"].(string)
			}
			c, err := parsePage(fixture(t, tc.Fixture), id)
			if tc.Error != "" {
				var classified *ParseError
				if !errors.As(err, &classified) || classified.Code != "UPSTREAM_RISK_CONTROL" {
					t.Fatalf("want classified risk control, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]any{"id": c.ID, "title": c.Title, "description": c.Description, "kind": c.Kind, "author": c.Author, "author_uid": c.AuthorUID, "source_url": c.SourceURL, "duration": c.Duration, "tags": c.Tags, "music_id": optionalText(c.MusicID), "music_title": optionalText(c.MusicTitle), "location": optionalText(c.Location), "is_deleted": c.IsDeleted, "is_private": c.IsPrivate, "manifest": c.Manifest}
			if !reflect.DeepEqual(jsonShape(t, got), tc.Content) {
				for key, want := range tc.Content {
					if value := jsonShape(t, got[key]); !reflect.DeepEqual(value, want) {
						t.Errorf("field %s differs: got %v, want %v", key, value, want)
					}
				}
			}
			if actual := buildPlan(c, maxVideoBytes, true); !reflect.DeepEqual(actual, tc.Plan) {
				t.Fatalf("plan differs: got %+v, want %+v", actual, tc.Plan)
			}
		})
	}
}
func TestReferencePlanningCapsAndOrder(t *testing.T) {
	c := Content{Manifest: Manifest{Images: []Image{}, Covers: []Image{{URL: "https://p.douyinpic.com/cover.webp"}}}}
	for i := 0; i < 70; i++ {
		c.Manifest.Images = append(c.Manifest.Images, Image{URL: fmt.Sprintf("https://p.douyinpic.com/%d.PNG?filename=x.exe", i)})
	}
	p := buildPlan(c, maxVideoBytes, true)
	if len(p.Items) != 64 || p.Items[63].Name != "image-64.png" || p.Items[0].MaxBytes != 32<<20 {
		t.Fatal("album cap or image naming differs")
	}
	c.Manifest.Images = c.Manifest.Images[:2]
	c.Manifest.Images[0].URL = "https://untrusted.invalid/no.jpg"
	p = buildPlan(c, 128, true)
	if len(p.Items) != 2 || p.Items[0].Name != "image-02.png" || p.Items[1].Name != "cover.webp" || p.Items[0].MaxBytes != 128 {
		t.Fatal("skipping renumbered slides or displaced cover", p)
	}
	payload := fixture(t, "video_normal")
	v, err := parsePage(payload, fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	p = buildPlan(v, maxVideoBytes, true)
	if len(p.Items[0].Mirrors) != 8 || strings.Contains(p.Items[0].Mirrors[0], "watermark") || !strings.Contains(p.Items[0].Mirrors[7], "watermark=1") {
		t.Fatal("reference mirror preference lost")
	}
}
func TestReferenceTransferContract(t *testing.T) {
	for _, tc := range []struct {
		name, kind, ct string
		body           []byte
		size, declared int64
		valid          bool
	}{
		{"webm", "video", "video/webm", []byte("webm-without-ftyp"), 100, -1, true},
		{"unknown length image", "image", "image/webp", []byte("image-bytes"), 100, -1, true},
		{"exact ceiling", "image", "image/png", []byte("12345678"), 8, 8, true},
		{"actual byte ceiling", "image", "image/png", []byte("123456789"), 8, -1, false},
		{"declared ceiling", "image", "image/png", []byte("1"), 8, 9, false},
		{"empty", "image", "image/jpeg", nil, 100, -1, false},
		{"html", "video", "text/html", []byte("<html>login"), 100, -1, false},
		{"image octet stream", "image", "application/octet-stream", []byte("not-a-typed-image"), 100, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			q := &Queue{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Cookie") != "" || r.Header.Get("User-Agent") != mediaUserAgent || r.Header.Get("Accept-Encoding") != "identity" {
					t.Error("media transfer headers differ")
				}
				if calls == 1 {
					return response(r, "text/html", []byte("wrong first mirror")), nil
				}
				resp := response(r, tc.ct, tc.body)
				resp.ContentLength = tc.declared
				return resp, nil
			})}}
			accept := []string{"image/"}
			if tc.kind == "video" {
				accept = []string{"video/", "application/octet-stream"}
			}
			item := PlannedItem{Name: "item", Kind: tc.kind, Mirrors: []string{"https://v.douyinvod.com/first", "https://v.douyinvod.com/second"}, Accept: accept, MaxBytes: tc.size}
			dest := filepath.Join(t.TempDir(), "item")
			got, err := q.fetch(context.Background(), item, dest, nil)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(tc.body)
				if got.SHA256 != hex.EncodeToString(sum[:]) || got.Bytes != int64(len(tc.body)) {
					t.Fatal("digest or byte count differs")
				}
			} else if err == nil {
				t.Fatal("invalid response saved")
			}
			if _, e := os.Stat(dest + ".part"); !os.IsNotExist(e) {
				t.Fatal("orphan partial")
			}
			if !tc.valid {
				if _, e := os.Stat(dest); !os.IsNotExist(e) {
					t.Fatal("refused file committed")
				}
			}
			if calls != 2 {
				t.Fatal("did not try mirrors sequentially", calls)
			}
		})
	}
}
func TestAlbumPartialRetryExportAndLocalPreview(t *testing.T) {
	s, err := NewServer(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	payload := fixture(t, "video_image_album")
	var item map[string]any
	_ = json.Unmarshal(payload, &item)
	id := textValue(object(item["aweme_detail"])["aweme_id"])
	s.resolver.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(r, "application/json", payload), nil })
	var fail atomic.Bool
	var failAll atomic.Bool
	fail.Store(true)
	var requests atomic.Int32
	image := []byte("synthetic-image-data")
	s.queue.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		if failAll.Load() || fail.Load() && strings.Contains(r.URL.Path, "synthetic-album-2") {
			return response(r, "text/html", []byte("login")), nil
		}
		return response(r, "image/jpeg", image), nil
	})
	h := s.Handler()
	w := call(t, h, "POST", "/api/parse", map[string]string{"text": "https://www.douyin.com/note/" + id}, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "douyinvod.com") {
		t.Fatal("signed image URL leaked")
	}
	var parsed struct {
		PreviewID string `json:"preview_id"`
		Content   Content
	}
	_ = json.Unmarshal(w.Body.Bytes(), &parsed)
	if parsed.Content.Kind != "image_album" || len(parsed.Content.Images) != 3 {
		t.Fatal("album preview missing")
	}
	w = call(t, h, "POST", "/api/tasks", map[string]string{"preview_id": parsed.PreviewID}, nil)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var task Task
	_ = json.Unmarshal(w.Body.Bytes(), &task)
	task = waitTask(t, s.store, task.ID, "partial")
	if task.FilesCompleted != 3 || task.FilesTotal != 4 {
		t.Fatalf("wrong partial result: %d/%d", task.FilesCompleted, task.FilesTotal)
	}
	before := requests.Load()
	w = call(t, h, "GET", "/api/tasks/"+task.ID+"/images/1", nil, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), image) || requests.Load() != before {
		t.Fatal("saved image was not served locally")
	}
	w = call(t, h, "GET", "/api/tasks/"+task.ID+"/images/2", nil, nil)
	if w.Code != 409 {
		t.Fatal("failed slide presented as saved", w.Code)
	}
	w = call(t, h, "GET", "/api/tasks/"+task.ID+"/archive", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), "partial.zip") {
		t.Fatal("partial archive not labelled")
	}
	archive, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if e != nil {
		t.Fatal(e)
	}
	if len(archive.File) != 5 {
		t.Fatal("archive includes failed file or lacks metadata", len(archive.File))
	}
	for _, file := range archive.File {
		f, e := file.Open()
		if e != nil {
			t.Fatal(e)
		}
		body, _ := io.ReadAll(f)
		f.Close()
		if strings.Contains(file.Name, "image-02") {
			t.Fatal("failed image exported")
		}
		if strings.HasSuffix(file.Name, "meta.json") && strings.Contains(string(body), "douyinvod.com") {
			t.Fatal("sidecar leaked signed URL")
		}
	}
	failAll.Store(true)
	w = call(t, h, "POST", "/api/tasks/"+task.ID+"/retry", nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	task = waitTask(t, s.store, task.ID, "partial")
	if task.Attempt != 2 || task.FilesCompleted != 3 {
		t.Fatal("failed retry lost previously saved slides")
	}
	for _, f := range task.Files {
		if f.State == "done" && !f.Retained {
			t.Fatal("retained result was not labelled")
		}
	}
	w = call(t, h, "GET", "/api/tasks/"+task.ID+"/images/1", nil, nil)
	if w.Code != 200 {
		t.Fatal("retained slide unavailable")
	}
	failAll.Store(false)
	fail.Store(false)
	w = call(t, h, "POST", "/api/tasks/"+task.ID+"/retry", nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	task = waitTask(t, s.store, task.ID, "completed")
	if task.FilesCompleted != 4 || task.Attempt != 3 {
		t.Fatal("retry did not recover all files")
	}
	w = call(t, h, "GET", "/api/tasks/"+task.ID+"/files/../shiying.db", nil, nil)
	if w.Code == 200 {
		t.Fatal("unsafe file path accepted")
	}
	w = call(t, h, "DELETE", "/api/tasks/"+task.ID, nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err = os.Stat(s.queue.taskDir(task.ID)); !os.IsNotExist(err) {
		t.Fatal("album directory survived delete")
	}
}
func TestURLParityAndSchemaFailure(t *testing.T) {
	for _, raw := range []string{"evildouyin.com/video/" + fixtureID, "xx.www.douyin.com/video/" + fixtureID, "https://evildouyin.com/video/" + fixtureID} {
		if _, e := extractLink(raw); e == nil {
			t.Fatal("partial host accepted", raw)
		}
	}
	for _, raw := range []string{"https://www.douyin.com/note/" + fixtureID, "douyin.com/slides/" + fixtureID, "https://www.douyin.com/user/author?modal_id=" + fixtureID} {
		link, e := extractLink(raw)
		if e != nil || contentID(link) != fixtureID {
			t.Fatal(raw, e)
		}
	}
	if validContentID("123") || validContentID("18446744073709551616") || !validContentID(fixtureID) {
		t.Fatal("ID validation differs")
	}
	c := map[string]any{}
	_ = json.Unmarshal(fixture(t, "video_normal"), &c)
	delete(object(c["aweme_detail"]), "status")
	raw, _ := json.Marshal(c)
	_, e := parsePage(raw, fixtureID)
	var classified *ParseError
	if !errors.As(e, &classified) || classified.Code != "UPSTREAM_CHANGED" {
		t.Fatal("missing required field was silently accepted", e)
	}
	for _, empty := range []any{nil, map[string]any{}, []any{}, "", false} {
		if e := riskControl(map[string]any{"captcha": empty}); e != nil {
			t.Fatal("empty captcha is truthy")
		}
	}
}

func TestV1DatabaseAndMP4Compatibility(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "shiying.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE tasks(id TEXT PRIMARY KEY,video_id TEXT NOT NULL UNIQUE,title TEXT NOT NULL,author TEXT NOT NULL,cover TEXT NOT NULL,duration INTEGER NOT NULL,width INTEGER NOT NULL,height INTEGER NOT NULL,source_url TEXT NOT NULL,media_urls TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'queued',downloaded INTEGER NOT NULL DEFAULT 0,total INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',favorite INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,attempt INTEGER NOT NULL DEFAULT 0);
 INSERT INTO settings VALUES('cookie','sessionid=legacy-test-secret');`)
	if err != nil {
		t.Fatal(err)
	}
	id := randomID()
	data := []byte("legacy-mp4-file-remains-intact")
	_, err = db.Exec(`INSERT INTO tasks(id,video_id,title,author,cover,duration,width,height,source_url,media_urls,status,downloaded,total,favorite,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, fixtureID, "legacy", "author", "", 1000, 720, 1280, "https://www.douyin.com/video/"+fixtureID, "[]", "completed", len(data), len(data), true, now(), now())
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = os.MkdirAll(filepath.Join(dir, "videos"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "videos", id+".mp4"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task, err := s.store.Task(id)
	if err != nil || task.Kind != "video" || !task.Favorite || task.Status != "completed" {
		t.Fatal("migration lost v1 metadata", err)
	}
	if cookie, e := s.store.Cookie(); e != nil || cookie != "sessionid=legacy-test-secret" {
		t.Fatal("migration lost settings")
	}
	w := call(t, s.Handler(), "GET", "/api/tasks/"+id+"/file?download=1", nil, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal("migration lost old MP4")
	}
	w = call(t, s.Handler(), "GET", "/api/tasks", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "legacy-test-secret") {
		t.Fatal("migration leaked setting")
	}
}

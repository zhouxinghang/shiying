package app

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"shiying/internal/web"
)

type preview struct {
	Video   Video
	Expires time.Time
}
type Server struct {
	credentialMu sync.RWMutex
	accountMu    sync.Mutex
	accountCache *accountSession
	store        *Store
	resolver     *Resolver
	queue        *Queue
	dataDir      string
	token        string
	mu           sync.Mutex
	previews     map[string]preview
	parseSlots   chan struct{}
	profileSlot  chan struct{}
	static       fs.FS
}

func NewServer(dataDir, token string) (*Server, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	store, err := OpenStore(dir)
	if err != nil {
		return nil, err
	}
	resolver := NewResolver(store)
	queue, err := NewQueue(store, resolver, filepath.Join(dir, "videos"))
	if err != nil {
		store.Close()
		return nil, err
	}
	static, _ := fs.Sub(web.Files, "static")
	return &Server{store: store, resolver: resolver, queue: queue, dataDir: dir, token: token, previews: make(map[string]preview), parseSlots: make(chan struct{}, 2), profileSlot: make(chan struct{}, 1), static: static}, nil
}
func (s *Server) Close() {
	s.queue.Close()
	if err := s.store.Close(); err != nil {
		slog.Error("close database", "error", err)
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "service": "shiying"})
	})
	mux.HandleFunc("POST /api/session", s.login)
	mux.HandleFunc("GET /api/settings", s.settings)
	mux.HandleFunc("PUT /api/settings", s.saveSettings)
	mux.HandleFunc("POST /api/parse", s.parse)
	mux.HandleFunc("GET /api/previews/{id}/cover", s.previewCover)
	mux.HandleFunc("GET /api/previews/{id}/images/{index}", s.previewImage)
	mux.HandleFunc("GET /api/tasks/{id}/images/{index}", s.taskImage)
	mux.HandleFunc("GET /api/tasks/{id}/files/{name}", s.namedFile)
	mux.HandleFunc("GET /api/tasks/{id}/archive", s.archive)
	mux.HandleFunc("GET /api/tasks", s.list)
	mux.HandleFunc("POST /api/tasks", s.add)
	mux.HandleFunc("POST /api/tasks/batch", s.batchAdd)
	mux.HandleFunc("POST /api/authors/resolve", s.resolveAuthor)
	mux.HandleFunc("GET /api/library/authors", s.libraryAuthors)
	mux.HandleFunc("GET /api/authors/{uid}/profile", s.authorProfile)
	mux.HandleFunc("GET /api/account", s.currentAccount)
	mux.HandleFunc("GET /api/account/likes", s.personalLikes)
	mux.HandleFunc("GET /api/account/bookmarks", s.personalBookmarks)
	mux.HandleFunc("GET /api/account/avatar", s.accountAvatar)
	mux.HandleFunc("GET /api/authors/{uid}/posts", s.authorPosts)
	mux.HandleFunc("GET /api/authors/{uid}/avatar", s.authorAvatar)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.delete)
	mux.HandleFunc("POST /api/tasks/{id}/cancel", s.cancelTask)
	mux.HandleFunc("POST /api/tasks/{id}/retry", s.retry)
	mux.HandleFunc("PATCH /api/tasks/{id}/favorite", s.favorite)
	mux.HandleFunc("GET /api/tasks/{id}/file", s.file)
	mux.HandleFunc("GET /api/tasks/{id}/cover", s.taskCover)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeError(w, 404, "接口不存在") })
	mux.HandleFunc("/", s.frontend)
	return s.middleware(mux)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, 400, "请求内容无效或过大")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, 400, "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}
func (s *Server) sessionValue() string {
	sum := sha256.Sum256([]byte("shiying-session:" + s.token))
	return hex.EncodeToString(sum[:])
}
func secureEqual(a, b string) bool {
	x := sha256.Sum256([]byte(a))
	y := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if s.token == "" {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if host != "localhost" && host != "127.0.0.1" && host != "[::1]" && host != "::1" {
				writeError(w, 403, "远程访问需要设置 SHIYING_ACCESS_TOKEN")
				return
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeError(w, 403, "不允许跨站请求")
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeError(w, 403, "不允许跨站请求")
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if s.token != "" && r.URL.Path != "/api/session" && r.URL.Path != "/api/health" {
				authorized := secureEqual(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), s.token)
				if cookie, err := r.Cookie("shiying_session"); err == nil && secureEqual(cookie.Value, s.sessionValue()) {
					authorized = true
				}
				if !authorized {
					writeError(w, 401, "请输入访问口令")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	if s.token != "" && !secureEqual(body.Token, s.token) {
		time.Sleep(250 * time.Millisecond)
		writeError(w, 401, "访问口令不正确")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "shiying_session", Value: s.sessionValue(), Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 7 * 24 * 3600})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	cookie, revision, err := s.store.Credentials()
	if err != nil {
		writeError(w, 500, "读取设置失败")
		return
	}
	writeJSON(w, 200, map[string]any{"cookie_configured": cookie != "", "cookie_revision": revision, "data_dir": s.dataDir, "concurrency": 2, "max_video_bytes": maxVideoBytes, "max_image_bytes": imageMaxBytes, "max_files": maxMediaItems, "version": "1.4.0"})
}
func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	var body struct {
		Cookie string `json:"cookie"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Cookie) > 8192 || strings.ContainsAny(body.Cookie, "\r\n\x00") {
		writeError(w, 400, "Cookie 应为单行内容，且不超过 8 KB")
		return
	}
	if err := s.store.SetCookie(strings.TrimSpace(body.Cookie)); err != nil {
		writeError(w, 500, "保存设置失败")
		return
	}
	s.settings(w, r)
}
func (s *Server) remember(v Video) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := time.Now()
	for id, p := range s.previews {
		if ts.After(p.Expires) {
			delete(s.previews, id)
		}
	}
	if len(s.previews) >= 100 {
		for id := range s.previews {
			delete(s.previews, id)
			break
		}
	}
	id := randomID()
	s.previews[id] = preview{v, ts.Add(30 * time.Minute)}
	return id
}
func (s *Server) getPreview(id string) (Video, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.previews[id]
	if ok && time.Now().Before(p.Expires) {
		return p.Video, true
	}
	c, err := s.store.CatalogContent(id)
	if err == nil {
		return c, true
	}
	c, err = s.store.PersonalContent(id)
	return c, err == nil
}
func (s *Server) parse(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	select {
	case s.parseSlots <- struct{}{}:
		defer func() { <-s.parseSlots }()
	default:
		writeError(w, 429, "正在解析其他链接，请稍后重试")
		return
	}
	v, err := s.resolver.Resolve(r.Context(), body.Text)
	if err != nil {
		var classified *ParseError
		if errors.As(err, &classified) {
			writeJSON(w, 422, map[string]string{"error": classified.Message, "code": classified.Code})
		} else {
			writeError(w, 422, err.Error())
		}
		return
	}
	if v.IsPrivate || v.IsDeleted {
		writeError(w, 422, "该作品已删除或不公开")
		return
	}
	id := s.remember(v)
	decorateContent(&v, "/api/previews/"+id)
	writeJSON(w, 200, map[string]any{"preview_id": id, "video": v, "content": v})
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.List()
	if err != nil {
		writeError(w, 500, "读取下载记录失败")
		return
	}
	var completed, active, failed int
	var total int64
	for i := range tasks {
		t := &tasks[i]
		decorateContent(&t.Video, "/api/tasks/"+t.ID)
		switch t.Status {
		case "completed":
			completed++
			total += t.Downloaded
		case "queued", "downloading":
			active++
		case "partial":
			total += t.Downloaded
			failed++
		case "failed":
			failed++
		}
	}
	writeJSON(w, 200, map[string]any{"tasks": tasks, "stats": map[string]any{"completed": completed, "active": active, "failed": failed, "total_bytes": total}})
}
func (s *Server) add(w http.ResponseWriter, r *http.Request) {
	s.credentialMu.RLock()
	defer s.credentialMu.RUnlock()
	var body struct {
		PreviewID string `json:"preview_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	v, ok := s.getPreview(body.PreviewID)
	if !ok {
		writeError(w, 400, "预览已过期，请重新解析链接")
		return
	}
	if existing, err := s.store.ByVideo(v.ID); err == nil {
		writeJSON(w, 409, map[string]any{"error": "这个作品已在下载记录中", "task_id": existing.ID})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, "读取记录失败")
		return
	}
	if reason := unavailableContent(v); reason != "" {
		writeError(w, 422, reason)
		return
	}
	t, err := s.store.Add(v)
	if err != nil {
		writeError(w, 409, "添加失败，作品可能已在记录中")
		return
	}
	decorateContent(&t.Video, "/api/tasks/"+t.ID)
	writeJSON(w, 201, t)
}
func taskError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "任务不存在")
	} else {
		writeError(w, 409, err.Error())
	}
}
func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	if err := s.queue.Cancel(r.PathValue("id")); err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) retry(w http.ResponseWriter, r *http.Request) {
	if err := s.queue.Retry(r.PathValue("id")); err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	if err := s.queue.Delete(r.PathValue("id")); err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) favorite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Favorite bool `json:"favorite"`
	}
	if !decode(w, r, &body) {
		return
	}
	result, err := s.store.db.Exec("UPDATE tasks SET favorite=?,updated_at=? WHERE id=?", body.Favorite, now(), r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "保存收藏失败")
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		writeError(w, 404, "任务不存在")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func decorateContent(c *Content, base string) {
	c.Cover = base + "/cover"
	c.Images = make([]ImagePreview, 0, len(c.Manifest.Images))
	for i, image := range c.Manifest.Images {
		c.Images = append(c.Images, ImagePreview{i + 1, base + "/images/" + strconv.Itoa(i+1), image.Width, image.Height})
	}
}
func available(t Task) bool { return t.Status == "completed" || t.Status == "partial" }
func (s *Server) serveStored(w http.ResponseWriter, r *http.Request, t Task, f StoredFile) {
	if !available(t) || f.State != "done" {
		writeError(w, 409, "这个文件尚未下载成功")
		return
	}
	name := f.Name
	if name != path.Base(name) || name == "." || name == ".." {
		writeError(w, 404, "文件不存在")
		return
	}
	filename := filepath.Join(s.queue.taskDir(t.ID), name)
	if len(t.Files) == 0 && f.Kind == "video" {
		filename = s.queue.file(t.ID)
	}
	file, err := os.Open(filename)
	if err != nil {
		writeError(w, 404, "本地文件不存在")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, 404, "本地文件不存在")
		return
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": exportName(t.Video.ID, name)}))
	}
	if f.ContentType != "" {
		w.Header().Set("Content-Type", f.ContentType)
	}
	http.ServeContent(w, r, name, info.ModTime(), file)
}
func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.Task(r.PathValue("id"))
	if err != nil {
		taskError(w, err)
		return
	}
	if t.Kind == "image_album" {
		s.archive(w, r)
		return
	}
	for _, f := range t.Files {
		if f.Kind == "video" {
			s.serveStored(w, r, t, f)
			return
		}
	}
	s.serveStored(w, r, t, StoredFile{Name: "video.mp4", Kind: "video", State: "done", ContentType: "video/mp4"})
}
func (s *Server) namedFile(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.Task(r.PathValue("id"))
	if err != nil {
		taskError(w, err)
		return
	}
	for _, f := range t.Files {
		if f.Name == r.PathValue("name") {
			s.serveStored(w, r, t, f)
			return
		}
	}
	if r.PathValue("name") == "meta.json" && len(t.Files) > 0 {
		s.serveStored(w, r, t, StoredFile{Name: "meta.json", State: "done", ContentType: "application/json"})
		return
	}
	writeError(w, 404, "文件不存在")
}
func (s *Server) archive(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.Task(r.PathValue("id"))
	if err != nil {
		taskError(w, err)
		return
	}
	if !available(t) {
		writeError(w, 409, "作品尚未下载完成")
		return
	}
	// Open all inputs before sending headers so a missing file is a clear error,
	// not a truncated ZIP claiming to be a completed export.
	type input struct {
		name string
		file *os.File
	}
	inputs := []input{}
	defer func() {
		for _, in := range inputs {
			in.file.Close()
		}
	}()
	for _, f := range t.Files {
		if f.State != "done" {
			continue
		}
		if f.Name != path.Base(f.Name) {
			writeError(w, 404, "文件不存在")
			return
		}
		file, e := os.Open(filepath.Join(s.queue.taskDir(t.ID), f.Name))
		if e != nil {
			writeError(w, 404, "本地文件缺失，请重新下载")
			return
		}
		inputs = append(inputs, input{f.Name, file})
	}
	if len(t.Files) == 0 {
		file, e := os.Open(s.queue.file(t.ID))
		if e != nil {
			writeError(w, 404, "本地文件不存在")
			return
		}
		inputs = append(inputs, input{"video.mp4", file})
	}
	if len(inputs) == 0 {
		writeError(w, 409, "没有可导出的成功文件")
		return
	}
	if file, e := os.Open(filepath.Join(s.queue.taskDir(t.ID), "meta.json")); e == nil {
		inputs = append(inputs, input{"meta.json", file})
	}
	name := exportName(t.Video.ID, "archive.zip")
	if t.Status == "partial" {
		name = exportName(t.Video.ID, "partial.zip")
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	archive := zip.NewWriter(w)
	for _, in := range inputs {
		entry, e := archive.CreateHeader(&zip.FileHeader{Name: exportName(t.Video.ID, in.name), Method: zip.Store})
		if e != nil {
			return
		}
		if _, e = io.Copy(entry, in.file); e != nil {
			return
		}
	}
	// This extra report describes partial exports without altering the reference
	// meta.json format or putting signed upstream URLs in an exported archive.
	report, e := archive.Create("download-report.json")
	if e == nil {
		_ = json.NewEncoder(report).Encode(map[string]any{"status": t.Status, "files": t.Files})
	}
	_ = archive.Close()
}
func (s *Server) previewImage(w http.ResponseWriter, r *http.Request) {
	c, ok := s.getPreview(r.PathValue("id"))
	if !ok {
		writeError(w, 404, "预览已过期")
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 1 || index > len(c.Manifest.Images) {
		writeError(w, 404, "图片不存在")
		return
	}
	s.proxyImage(w, r, c.Manifest.Images[index-1])
}
func (s *Server) taskImage(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.Task(r.PathValue("id"))
	if err != nil {
		taskError(w, err)
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 1 || index > len(t.Manifest.Images) {
		writeError(w, 404, "图片不存在")
		return
	}
	prefix := fmt.Sprintf("image-%02d.", index)
	if available(t) {
		for _, f := range t.Files {
			if f.Kind == "image" && strings.HasPrefix(f.Name, prefix) {
				s.serveStored(w, r, t, f)
				return
			}
		}
		writeError(w, 404, "这张图片未下载成功")
		return
	}
	s.proxyImage(w, r, t.Manifest.Images[index-1])
}
func (s *Server) proxyImage(w http.ResponseWriter, r *http.Request, image Image) {
	mirrors, _ := sourceMirrors(image)
	s.proxyImages(w, r, mirrors)
}
func (s *Server) taskCover(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.Task(r.PathValue("id"))
	if err != nil {
		taskError(w, err)
		return
	}
	if available(t) {
		for _, kind := range []string{"cover", "image"} {
			for _, f := range t.Files {
				if f.Kind == kind && f.State == "done" {
					s.serveStored(w, r, t, f)
					return
				}
			}
		}
	}
	if len(t.Manifest.Covers) > 0 {
		s.proxyImage(w, r, t.Manifest.Covers[0])
		return
	}
	if len(t.Manifest.Images) > 0 {
		s.proxyImage(w, r, t.Manifest.Images[0])
		return
	}
	s.cover(w, r, t.Cover)
}
func (s *Server) previewCover(w http.ResponseWriter, r *http.Request) {
	v, ok := s.getPreview(r.PathValue("id"))
	if !ok {
		writeError(w, 404, "预览已过期")
		return
	}
	if len(v.Manifest.Covers) > 0 {
		s.proxyImage(w, r, v.Manifest.Covers[0])
		return
	}
	if len(v.Manifest.Images) > 0 {
		s.proxyImage(w, r, v.Manifest.Images[0])
		return
	}
	s.cover(w, r, v.Cover)
}
func (s *Server) cover(w http.ResponseWriter, r *http.Request, raw string) {
	s.proxyImages(w, r, []string{normalizeMediaURL(raw)})
}
func (s *Server) proxyImages(w http.ResponseWriter, r *http.Request, mirrors []string) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	for _, raw := range mirrors {
		resp, err := request(ctx, s.queue.client, raw, "", mediaDomains, http.Header{"User-Agent": {mediaUserAgent}, "Accept": {"*/*"}, "Accept-Encoding": {"identity"}})
		if err != nil {
			continue
		}
		kind := strings.ToLower(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
		allowed := kind == "image/jpeg" || kind == "image/png" || kind == "image/webp" || kind == "image/avif" || kind == "image/gif" || kind == "image/heic" || kind == "image/heif"
		if resp.StatusCode != 200 || !allowed || resp.ContentLength > imageMaxBytes {
			resp.Body.Close()
			continue
		}
		body, e := io.ReadAll(io.LimitReader(resp.Body, imageMaxBytes+1))
		resp.Body.Close()
		if e != nil || len(body) == 0 || int64(len(body)) > imageMaxBytes {
			continue
		}
		w.Header().Set("Content-Type", kind)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write(body)
		return
	}
	writeError(w, 502, "图片暂不可用，可重新解析或下载后查看")
}
func (s *Server) frontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	body, err := fs.ReadFile(s.static, name)
	if err != nil && !strings.Contains(path.Base(name), ".") {
		name = "index.html"
		body, err = fs.ReadFile(s.static, name)
	}
	if err != nil {
		if name == "index.html" {
			http.Error(w, "请先构建前端：cd personal/ui && npm ci && npm run build", 503)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	_, _ = w.Write(body)
}

func ValidateListen(addr, token string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if token == "" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return fmt.Errorf("监听非本地地址时必须设置 SHIYING_ACCESS_TOKEN")
	}
	return nil
}

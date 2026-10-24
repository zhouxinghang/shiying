package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var authorUIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{8,256}$`)
var authorPathPattern = regexp.MustCompile(`^/(?:share/)?user/([A-Za-z0-9_.-]+)/*$`)
var cursorPattern = regexp.MustCompile(`^[0-9]{1,20}$`)

type AuthorProfile struct {
	UID           string `json:"uid"`
	Nickname      string `json:"nickname"`
	Signature     string `json:"signature"`
	Avatar        string `json:"avatar"`
	WebURL        string `json:"web_url"`
	WorksCount    *int64 `json:"works_count"`
	FollowerCount *int64 `json:"follower_count"`
	AvatarImage   *Image `json:"-"`
}
type authorRecord struct {
	Profile AuthorProfile `json:"profile"`
	Image   *Image        `json:"image"`
}
type catalogRecord struct {
	Content   Content  `json:"content"`
	Manifest  Manifest `json:"media"`
	MediaURLs []string `json:"media_urls"`
}
type AuthorPostPage struct {
	Items   []Content `json:"items"`
	Cursor  string    `json:"cursor"`
	HasMore bool      `json:"has_more"`
}
type AuthorPost struct {
	Content      Content `json:"content"`
	PreviewID    string  `json:"preview_id"`
	TaskID       string  `json:"task_id,omitempty"`
	TaskStatus   string  `json:"task_status,omitempty"`
	Downloadable bool    `json:"downloadable"`
	Reason       string  `json:"reason,omitempty"`
}

func authorUID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !allowedURL(raw, pageDomains) {
		return ""
	}
	if m := authorPathPattern.FindStringSubmatch(u.Path); len(m) > 1 && authorUIDPattern.MatchString(m[1]) {
		return m[1]
	}
	for _, key := range []string{"sec_user_id", "sec_uid"} {
		if id := u.Query().Get(key); authorUIDPattern.MatchString(id) {
			return id
		}
	}
	return ""
}
func (r *Resolver) webJSON(ctx context.Context, endpoint string, params []queryParam) (map[string]any, error) {
	cookie, err := r.store.Cookie()
	if err != nil {
		return nil, fmt.Errorf("读取 Cookie 配置失败")
	}
	return r.webJSONWithCookie(ctx, http.MethodGet, endpoint, params, cookie)
}
func (r *Resolver) webJSONWithCookie(ctx context.Context, method, endpoint string, params []queryParam, cookie string) (map[string]any, error) {
	if cookie == "" {
		return nil, &ParseError{"COOKIE_REQUIRED", "请先在偏好设置中配置完整抖音登录 Cookie"}
	}
	target, signed := signedAPIURL(endpoint, params, cookie, time.Now().UnixMilli())
	headers := http.Header{"User-Agent": {desktopUA}, "Accept": {"application/json"}}
	for key, value := range signed {
		headers.Set(key, value)
	}
	resp, err := requestMethod(ctx, r.client, method, target, cookie, pageDomains, headers)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("连接抖音接口失败，请稍后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logUpstreamHTTPFailure(resp)
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 {
		return nil, &ParseError{"UPSTREAM_RISK_CONTROL", fmt.Sprintf("抖音接口返回 HTTP %d，请更新 Cookie 或稍后重试", resp.StatusCode)}
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("抖音接口返回 HTTP %d", resp.StatusCode)
	}
	body, err := readUpstreamBody(resp, 16<<20)
	if err != nil {
		return nil, fmt.Errorf("读取抖音数据失败或响应过大")
	}
	var root map[string]any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err = dec.Decode(&root); err != nil || len(root) == 0 {
		logUpstreamFailure(resp, "decode_json", body, err)
		return nil, &ParseError{"UPSTREAM_RISK_CONTROL", "抖音返回空数据或验证页面，请更新 Cookie 或稍后重试"}
	}
	if err = riskControl(root); err != nil {
		logUpstreamFailure(resp, "risk_control", body, err)
		return nil, err
	}
	if code := number(root["status_code"]); code != 0 {
		err = &ParseError{"UPSTREAM_BUSINESS_ERROR", fmt.Sprintf("抖音接口未返回数据（状态码 %d），请确认 Cookie 有效且页面可访问", code)}
		logUpstreamFailure(resp, "business_status", body, err)
		return nil, err
	}
	return root, nil
}
func parseAuthorProfile(root map[string]any, uid string) (AuthorProfile, error) {
	if err := riskControl(root); err != nil {
		return AuthorProfile{}, err
	}
	user := object(root["user"])
	if user == nil || textValue(user["sec_uid"]) == "" {
		return AuthorProfile{}, &ParseError{"UPSTREAM_CHANGED", "作者数据缺少 user.sec_uid"}
	}
	if textValue(user["sec_uid"]) != uid {
		return AuthorProfile{}, &ParseError{"UPSTREAM_CHANGED", "返回的作者与请求不匹配"}
	}
	nickname, ok := user["nickname"].(string)
	if !ok {
		return AuthorProfile{}, &ParseError{"UPSTREAM_CHANGED", "作者数据缺少 user.nickname"}
	}
	profile := AuthorProfile{UID: uid, Nickname: nickname, Signature: textValue(user["signature"]), WebURL: "https://www.douyin.com/user/" + uid, WorksCount: nullableInt(user["aweme_count"]), FollowerCount: nullableInt(user["follower_count"])}
	for _, key := range []string{"avatar_larger", "avatar_medium", "avatar_300x300", "avatar_thumb"} {
		if image := imageFromNode(user[key]); image != nil {
			profile.AvatarImage = image
			break
		}
	}
	return profile, nil
}
func parseAuthorPosts(root map[string]any) (AuthorPostPage, error) {
	return parseContentPage(root, "max_cursor")
}
func parseContentPage(root map[string]any, cursorKey string) (AuthorPostPage, error) {
	page := AuthorPostPage{Items: []Content{}}
	if err := riskControl(root); err != nil {
		return page, err
	}
	list, ok := root["aweme_list"].([]any)
	if !ok {
		return page, &ParseError{"UPSTREAM_CHANGED", "作者作品数据缺少 aweme_list"}
	}
	if root["has_more"] == nil {
		return page, &ParseError{"UPSTREAM_CHANGED", "作者作品数据缺少 has_more"}
	}
	page.HasMore = boolValue(root["has_more"])
	if page.HasMore {
		page.Cursor = textValue(root[cursorKey])
		if !cursorPattern.MatchString(page.Cursor) || strings.TrimLeft(page.Cursor, "0") == "" {
			return page, &ParseError{"UPSTREAM_CHANGED", "作品声明还有下一页，但没有有效的 " + cursorKey}
		}
	}
	for _, entry := range list {
		item := object(entry)
		if item == nil {
			return page, &ParseError{"UPSTREAM_CHANGED", "作者作品列表包含无效条目"}
		}
		if err := validateDetail(item); err != nil {
			return page, err
		}
		id := textValue(item["aweme_id"])
		if !validContentID(id) {
			return page, &ParseError{"UPSTREAM_CHANGED", "作者作品包含无效的作品 ID"}
		}
		c, err := parseVideo(item, id)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, c)
	}
	return page, nil
}
func (r *Resolver) ResolveAuthor(ctx context.Context, text string) (AuthorProfile, error) {
	link, err := extractLink(text)
	if err != nil {
		return AuthorProfile{}, err
	}
	cookie, cookieErr := r.store.Cookie()
	if cookieErr != nil {
		return AuthorProfile{}, fmt.Errorf("读取 Cookie 配置失败")
	}
	if cookie == "" {
		return AuthorProfile{}, &ParseError{"COOKIE_REQUIRED", "查看作者作品需要先在偏好设置中配置完整抖音 Cookie"}
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	uid := authorUID(link)
	if uid == "" && contentID(link) == "" {
		if link, err = r.expand(ctx, link); err != nil {
			return AuthorProfile{}, err
		}
		uid = authorUID(link)
	}
	if uid == "" {
		if contentID(link) == "" {
			return AuthorProfile{}, &ParseError{"INVALID_URL", "请粘贴作者主页或该作者的一条作品分享链接"}
		}
		c, e := r.Resolve(ctx, link)
		if e != nil {
			return AuthorProfile{}, e
		}
		uid = c.AuthorUID
	}
	if !authorUIDPattern.MatchString(uid) || !strings.HasPrefix(uid, "MS4") {
		return AuthorProfile{}, &ParseError{"AUTHOR_ID_MISSING", "作品未提供可用的作者 sec_uid，请复制作者主页链接后重试"}
	}
	root, err := r.webJSON(ctx, "/aweme/v1/web/user/profile/other/", []queryParam{{"sec_user_id", uid}, {"publish_video_strategy_type", "2"}, {"personal_center_strategy", "1"}})
	if err != nil {
		return AuthorProfile{}, err
	}
	return parseAuthorProfile(root, uid)
}
func (r *Resolver) AuthorPosts(ctx context.Context, uid, cursor string) (AuthorPostPage, error) {
	if !authorUIDPattern.MatchString(uid) || !strings.HasPrefix(uid, "MS4") {
		return AuthorPostPage{}, &ParseError{"INVALID_PARAM", "作者 ID 无效"}
	}
	if cursor == "" {
		cursor = "0"
	}
	if !cursorPattern.MatchString(cursor) {
		return AuthorPostPage{}, &ParseError{"INVALID_PARAM", "分页游标无效"}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Exact order and defaults from platforms/douyin/params.py:author_posts_params.
	params := []queryParam{{"sec_user_id", uid}, {"max_cursor", cursor}, {"count", "20"}, {"publish_video_strategy_type", "2"}, {"from_user_page", "1"}, {"locate_query", "false"}, {"need_time_list", "1"}, {"show_live_replay_strategy", "1"}, {"time_list_query", "0"}, {"pc_libra_divert", "Windows"}, {"whale_cut_token", ""}}
	root, err := r.webJSON(ctx, "/aweme/v1/web/aweme/post/", params)
	if err != nil {
		return AuthorPostPage{}, err
	}
	page, err := parseAuthorPosts(root)
	if err == nil && page.HasMore && page.Cursor == cursor {
		err = &ParseError{"UPSTREAM_CHANGED", "作者作品分页游标未前进，已停止加载，避免重复请求"}
	}
	return page, err
}
func (s *Store) SaveAuthor(profile AuthorProfile) error {
	raw, err := json.Marshal(authorRecord{profile, profile.AvatarImage})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO authors(uid,payload,updated_at)VALUES(?,?,?) ON CONFLICT(uid)DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, profile.UID, string(raw), now())
	return err
}
func (s *Store) Author(uid string) (AuthorProfile, error) {
	var raw string
	err := s.db.QueryRow("SELECT payload FROM authors WHERE uid=?", uid).Scan(&raw)
	if err != nil {
		return AuthorProfile{}, err
	}
	var row authorRecord
	err = json.Unmarshal([]byte(raw), &row)
	row.Profile.AvatarImage = row.Image
	return row.Profile, err
}
func (s *Store) SaveCatalog(uid string, c Content) (string, error) {
	raw, err := json.Marshal(catalogRecord{c, c.Manifest, c.MediaURLs})
	if err != nil {
		return "", err
	}
	var key string
	err = s.db.QueryRow(`INSERT INTO author_catalog(preview_id,content_id,author_uid,payload,updated_at)VALUES(?,?,?,?,?) ON CONFLICT(content_id)DO UPDATE SET author_uid=excluded.author_uid,payload=excluded.payload,updated_at=excluded.updated_at RETURNING preview_id`, randomID(), c.ID, uid, string(raw), now()).Scan(&key)
	return key, err
}
func (s *Store) CatalogContent(id string) (Content, error) {
	var raw string
	if err := s.db.QueryRow("SELECT payload FROM author_catalog WHERE preview_id=?", id).Scan(&raw); err != nil {
		return Content{}, err
	}
	var row catalogRecord
	err := json.Unmarshal([]byte(raw), &row)
	row.Content.Manifest = row.Manifest
	row.Content.MediaURLs = row.MediaURLs
	return row.Content, err
}
func unavailableContent(c Content) string {
	if c.IsDeleted {
		return "作品已删除"
	}
	if c.IsPrivate {
		return "作品不公开"
	}
	for _, item := range buildPlan(c, maxVideoBytes, true).Items {
		if item.Kind == "video" || item.Kind == "image" {
			return ""
		}
	}
	return "作品没有可下载的媒体"
}
func upstreamError(w http.ResponseWriter, err error) {
	var classified *ParseError
	if errors.As(err, &classified) {
		writeJSON(w, 422, map[string]string{"error": classified.Message, "code": classified.Code})
		return
	}
	if errors.Is(err, context.Canceled) {
		writeError(w, 408, "请求已取消")
		return
	}
	writeError(w, 502, err.Error())
}
func (s *Server) resolveAuthor(w http.ResponseWriter, r *http.Request) {
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
	profile, err := s.resolver.ResolveAuthor(r.Context(), body.Text)
	if err != nil {
		upstreamError(w, err)
		return
	}
	if err = s.store.SaveAuthor(profile); err != nil {
		writeError(w, 500, "保存作者信息失败")
		return
	}
	profile.Avatar = "/api/authors/" + profile.UID + "/avatar"
	writeJSON(w, 200, profile)
}
func (s *Server) authorPosts(w http.ResponseWriter, r *http.Request) {
	select {
	case s.parseSlots <- struct{}{}:
		defer func() { <-s.parseSlots }()
	default:
		writeError(w, 429, "正在加载其他作者作品，请稍后重试")
		return
	}
	page, err := s.resolver.AuthorPosts(r.Context(), r.PathValue("uid"), r.URL.Query().Get("cursor"))
	if err != nil {
		upstreamError(w, err)
		return
	}
	items := make([]AuthorPost, 0, len(page.Items))
	seen := map[string]bool{}
	for _, c := range page.Items {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		key, e := s.store.SaveCatalog(r.PathValue("uid"), c)
		if e != nil {
			writeError(w, 500, "保存作者作品失败")
			return
		}
		post := AuthorPost{Content: c, PreviewID: key, Reason: unavailableContent(c)}
		post.Downloadable = post.Reason == ""
		if task, e := s.store.ByVideo(c.ID); e == nil {
			post.TaskID = task.ID
			post.TaskStatus = task.Status
		} else if !errors.Is(e, sql.ErrNoRows) {
			writeError(w, 500, "读取下载记录失败")
			return
		}
		decorateContent(&post.Content, "/api/previews/"+key)
		items = append(items, post)
	}
	writeJSON(w, 200, map[string]any{"items": items, "cursor": page.Cursor, "has_more": page.HasMore})
}
func (s *Server) authorAvatar(w http.ResponseWriter, r *http.Request) {
	profile, err := s.store.Author(r.PathValue("uid"))
	if err != nil || profile.AvatarImage == nil {
		writeError(w, 404, "作者头像暂不可用")
		return
	}
	s.proxyImage(w, r, *profile.AvatarImage)
}

type batchResult struct {
	PreviewID string `json:"preview_id"`
	TaskID    string `json:"task_id,omitempty"`
	Title     string `json:"title,omitempty"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
}

func (s *Server) batchAdd(w http.ResponseWriter, r *http.Request) {
	s.credentialMu.RLock()
	defer s.credentialMu.RUnlock()
	var body struct {
		IDs []string `json:"preview_ids"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 100 {
		writeError(w, 400, "每批请选择 1–100 个作品")
		return
	}
	results := make([]batchResult, 0, len(body.IDs))
	seen := map[string]bool{}
	for _, key := range body.IDs {
		if seen[key] {
			continue
		}
		seen[key] = true
		result := batchResult{PreviewID: key, State: "failed"}
		c, ok := s.getPreview(key)
		if !ok {
			result.Error = "作品预览不存在或账号已切换，请重新加载作品"
			results = append(results, result)
			continue
		}
		result.Title = c.Title
		if existing, e := s.store.ByVideo(c.ID); e == nil {
			result.State = "existing"
			result.TaskID = existing.ID
			results = append(results, result)
			continue
		} else if !errors.Is(e, sql.ErrNoRows) {
			result.Error = "读取下载记录失败"
			results = append(results, result)
			continue
		}
		if reason := unavailableContent(c); reason != "" {
			result.Error = reason
			results = append(results, result)
			continue
		}
		task, e := s.store.Add(c)
		if e != nil {
			if existing, e := s.store.ByVideo(c.ID); e == nil {
				result.State = "existing"
				result.TaskID = existing.ID
			} else {
				result.Error = "加入下载队列失败，请重试"
			}
		} else {
			result.State = "added"
			result.TaskID = task.ID
		}
		results = append(results, result)
	}
	writeJSON(w, 200, map[string]any{"results": results})
}

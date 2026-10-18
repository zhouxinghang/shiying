package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// query/user names guests too. Only profile/self, with actual session cookies,
// may establish the subject of a personal feed. No caller-supplied author ID is
// accepted for these routes, and every upstream request uses one cookie snapshot.
type accountSession struct {
	Profile        AuthorProfile `json:"profile"`
	Scope          string        `json:"scope"`
	LikesCount     *int64        `json:"likes_count"`
	BookmarksCount *int64        `json:"bookmarks_count"`
	NumericUID     string        `json:"-"`
	Cookie         string        `json:"-"`
	Expires        time.Time     `json:"-"`
}

func loginCookie(cookie string) bool {
	values := cookieValues(cookie)
	for _, key := range []string{"sessionid", "sessionid_ss", "sid_tt"} {
		if values[key] != "" {
			return true
		}
	}
	return false
}
func accountChanged() error {
	return &ParseError{"ACCOUNT_CHANGED", "Cookie 已更换，请重新读取当前账号后再加载或下载"}
}

// Complete a personal response against the same credentials that produced it.
// Network operations don't hold this lock, so settings remain responsive.
func (s *Server) writeAccountJSON(w http.ResponseWriter, scope string, value any) {
	s.credentialMu.RLock()
	defer s.credentialMu.RUnlock()
	_, current, err := s.store.Credentials()
	if err != nil {
		writeError(w, 500, "核对账号设置失败")
		return
	}
	if current != scope {
		upstreamError(w, accountChanged())
		return
	}
	writeJSON(w, 200, value)
}
func parseSelf(root map[string]any) (accountSession, error) {
	user := object(root["user"])
	uid := textValue(user["sec_uid"])
	numeric := textValue(user["uid"])
	if number(root["status_code"]) != 0 || !authorUIDPattern.MatchString(uid) || !strings.HasPrefix(uid, "MS4") || !idPattern.MatchString(numeric) || strings.TrimLeft(numeric, "0") == "" {
		return accountSession{}, &ParseError{"ACCOUNT_UNVERIFIED", "未确认 Cookie 的登录账号，请更新完整登录 Cookie 后重试"}
	}
	profile, err := parseAuthorProfile(root, uid)
	if err != nil {
		return accountSession{}, err
	}
	return accountSession{Profile: profile, NumericUID: numeric, LikesCount: nullableInt(user["favoriting_count"]), BookmarksCount: nullableInt(root["user_collect_count"])}, nil
}
func (s *Server) accountFor(ctx context.Context, expected string) (accountSession, error) {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	cookie, scope, err := s.store.Credentials()
	if err != nil {
		return accountSession{}, fmt.Errorf("读取账号设置失败")
	}
	if expected != "" && expected != scope {
		return accountSession{}, accountChanged()
	}
	if cookie == "" {
		return accountSession{}, &ParseError{"COOKIE_REQUIRED", "请先在偏好设置中配置完整抖音登录 Cookie"}
	}
	if !loginCookie(cookie) {
		return accountSession{}, &ParseError{"ACCOUNT_UNVERIFIED", "当前 Cookie 不含登录会话，无法读取本人的喜欢和收藏，请复制登录后的完整 Cookie"}
	}
	if s.accountCache != nil && s.accountCache.Scope == scope && s.accountCache.Expires.After(time.Now()) {
		return *s.accountCache, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	root, err := s.resolver.webJSONWithCookie(ctx, http.MethodGet, "/aweme/v1/web/user/profile/self/", nil, cookie)
	if err != nil {
		return accountSession{}, err
	}
	account, err := parseSelf(root)
	if err != nil {
		return accountSession{}, err
	}
	if _, current, e := s.store.Credentials(); e != nil {
		return accountSession{}, fmt.Errorf("核对当前账号失败")
	} else if current != scope {
		return accountSession{}, accountChanged()
	}
	account.Scope = scope
	account.Cookie = cookie
	account.Expires = time.Now().Add(2 * time.Minute)
	account.Profile.Avatar = "/api/account/avatar?scope=" + url.QueryEscape(scope)
	s.accountCache = &account
	return account, nil
}
func (s *Store) SavePersonal(scope string, c Content) (string, error) {
	raw, err := json.Marshal(catalogRecord{c, c.Manifest, c.MediaURLs})
	if err != nil {
		return "", err
	}
	var key string
	err = s.db.QueryRow(`INSERT INTO personal_catalog(preview_id,scope,content_id,payload,updated_at) SELECT ?,?,?,?,? WHERE EXISTS(SELECT 1 FROM settings WHERE key='cookie_revision' AND value=?) ON CONFLICT(scope,content_id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at RETURNING preview_id`, randomID(), scope, c.ID, string(raw), now(), scope).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", accountChanged()
	}
	return key, err
}
func (s *Store) PersonalContent(id string) (Content, error) {
	var raw string
	err := s.db.QueryRow(`SELECT payload FROM personal_catalog WHERE preview_id=? AND scope=(SELECT value FROM settings WHERE key='cookie_revision')`, id).Scan(&raw)
	if err != nil {
		return Content{}, err
	}
	var row catalogRecord
	err = json.Unmarshal([]byte(raw), &row)
	row.Content.Manifest = row.Manifest
	row.Content.MediaURLs = row.MediaURLs
	return row.Content, err
}
func personalCursor(raw string) (string, error) {
	if raw == "" {
		return "0", nil
	}
	if !cursorPattern.MatchString(raw) {
		return "", &ParseError{"INVALID_PARAM", "分页游标无效"}
	}
	return raw, nil
}
func (r *Resolver) personalFeed(ctx context.Context, account accountSession, kind, cursor string) (AuthorPostPage, string, error) {
	next, err := personalCursor(cursor)
	if err != nil {
		return AuthorPostPage{}, "", err
	}
	method, endpoint, key := http.MethodGet, "/aweme/v1/web/aweme/favorite/", "max_cursor"
	params := []queryParam{{"sec_user_id", account.Profile.UID}, {"max_cursor", next}, {"count", "20"}, {"publish_video_strategy_type", "2"}}
	if kind == "bookmarks" {
		method = http.MethodPost
		endpoint = "/aweme/v1/web/aweme/listcollection/"
		key = "cursor"
		params = []queryParam{{"cursor", next}, {"count", "20"}}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	root, err := r.webJSONWithCookie(ctx, method, endpoint, params, account.Cookie)
	if err != nil {
		return AuthorPostPage{}, "", err
	}
	// Personal endpoints often echo their owner. An echoed stranger is never a
	// successful response, even if it contains a perfectly valid public list.
	if uid := textValue(root["sec_uid"]); uid != "" && uid != account.Profile.UID {
		return AuthorPostPage{}, "", &ParseError{"ACCOUNT_MISMATCH", "平台返回的列表不属于当前 Cookie 账号，已停止读取"}
	}
	if uid := textValue(root["uid"]); uid != "" && uid != account.NumericUID {
		return AuthorPostPage{}, "", &ParseError{"ACCOUNT_MISMATCH", "平台返回的账号与当前 Cookie 不一致，已停止读取"}
	}
	page, err := parseContentPage(root, key)
	if err != nil {
		return page, "", err
	}
	if page.HasMore && page.Cursor == next {
		return page, "", &ParseError{"UPSTREAM_CHANGED", "个人作品分页游标未前进，已停止加载，已加载内容仍可使用"}
	}
	notice := ""
	if count := number(root["invalid_item_count"]); count > 0 {
		notice = fmt.Sprintf("平台标记 %d 个收藏作品已失效，已按平台返回的可用作品加载。", count)
	}
	return page, notice, nil
}
func accountQuery(r *http.Request) error {
	for key := range r.URL.Query() {
		if key != "scope" && key != "cursor" {
			return &ParseError{"INVALID_PARAM", "个人喜欢和收藏只读取当前 Cookie 用户，不接受指定用户参数"}
		}
	}
	return nil
}
func (s *Server) currentAccount(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, 400, "账号由 Cookie 确定，不接受用户参数")
		return
	}
	select {
	case s.parseSlots <- struct{}{}:
		defer func() { <-s.parseSlots }()
	default:
		writeError(w, 429, "正在读取其他数据，请稍后重试")
		return
	}
	account, err := s.accountFor(r.Context(), "")
	if err != nil {
		upstreamError(w, err)
		return
	}
	s.writeAccountJSON(w, account.Scope, account)
}
func (s *Server) personalLikes(w http.ResponseWriter, r *http.Request) {
	s.personalWorks(w, r, "likes")
}
func (s *Server) personalBookmarks(w http.ResponseWriter, r *http.Request) {
	s.personalWorks(w, r, "bookmarks")
}
func (s *Server) personalWorks(w http.ResponseWriter, r *http.Request, kind string) {
	if err := accountQuery(r); err != nil {
		upstreamError(w, err)
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		writeError(w, 400, "请先读取当前 Cookie 账号")
		return
	}
	select {
	case s.parseSlots <- struct{}{}:
		defer func() { <-s.parseSlots }()
	default:
		writeError(w, 429, "正在读取其他数据，请稍后重试")
		return
	}
	account, err := s.accountFor(r.Context(), scope)
	if err != nil {
		upstreamError(w, err)
		return
	}
	page, notice, err := s.resolver.personalFeed(r.Context(), account, kind, r.URL.Query().Get("cursor"))
	if err != nil {
		upstreamError(w, err)
		return
	}
	_, current, e := s.store.Credentials()
	if e != nil {
		writeError(w, 500, "核对账号设置失败")
		return
	}
	if current != scope {
		upstreamError(w, accountChanged())
		return
	}
	items := make([]AuthorPost, 0, len(page.Items))
	seen := map[string]bool{}
	for _, c := range page.Items {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		key, e := s.store.SavePersonal(scope, c)
		if e != nil {
			upstreamError(w, e)
			return
		}
		item := AuthorPost{Content: c, PreviewID: key, Reason: unavailableContent(c)}
		item.Downloadable = item.Reason == ""
		if task, e := s.store.ByVideo(c.ID); e == nil {
			item.TaskID = task.ID
			item.TaskStatus = task.Status
		} else if !errors.Is(e, sql.ErrNoRows) {
			writeError(w, 500, "读取下载记录失败")
			return
		}
		decorateContent(&item.Content, "/api/previews/"+key)
		items = append(items, item)
	}
	s.writeAccountJSON(w, scope, map[string]any{"items": items, "cursor": page.Cursor, "has_more": page.HasMore, "notice": notice, "account_scope": scope})
}
func (s *Server) accountAvatar(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		writeError(w, 400, "缺少当前账号标识")
		return
	}
	account, err := s.accountFor(r.Context(), scope)
	if err != nil {
		upstreamError(w, err)
		return
	}
	if account.Profile.AvatarImage == nil {
		writeError(w, 404, "账号头像暂不可用")
		return
	}
	s.proxyImage(w, r, *account.Profile.AvatarImage)
}

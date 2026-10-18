package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const authorProfileTTL = 24 * time.Hour

type cachedAuthorProfile struct {
	Profile     AuthorProfile `json:"profile"`
	FetchedAt   string        `json:"fetched_at"`
	Stale       bool          `json:"stale"`
	Warning     string        `json:"warning,omitempty"`
	WarningCode string        `json:"warning_code,omitempty"`
}

func cachedProfile(raw, fetched string) (cachedAuthorProfile, error) {
	var row authorRecord
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		return cachedAuthorProfile{}, err
	}
	row.Profile.AvatarImage = row.Image
	row.Profile.Avatar = ""
	if row.Image != nil {
		row.Profile.Avatar = "/api/authors/" + row.Profile.UID + "/avatar?v=" + url.QueryEscape(fetched)
	}
	ts, err := time.Parse(time.RFC3339Nano, fetched)
	return cachedAuthorProfile{Profile: row.Profile, FetchedAt: fetched, Stale: err != nil || time.Since(ts) > authorProfileTTL}, nil
}

func (s *Store) CachedAuthor(uid string) (cachedAuthorProfile, error) {
	var raw, fetched string
	if err := s.db.QueryRow("SELECT payload,updated_at FROM authors WHERE uid=?", uid).Scan(&raw, &fetched); err != nil {
		return cachedAuthorProfile{}, err
	}
	return cachedProfile(raw, fetched)
}

// Return cached public profiles only for creators represented in local saved
// works. This endpoint never contacts Douyin or exposes signed avatar URLs.
func (s *Server) libraryAuthors(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.List()
	if err != nil {
		writeError(w, 500, "读取本地作者失败")
		return
	}
	wanted := map[string]bool{}
	for _, task := range tasks {
		if task.Status == "completed" || task.Status == "partial" {
			wanted[task.AuthorUID] = true
		}
	}
	rows, err := s.store.db.Query("SELECT uid,payload,updated_at FROM authors")
	if err != nil {
		writeError(w, 500, "读取作者资料缓存失败")
		return
	}
	defer rows.Close()
	items := []cachedAuthorProfile{}
	for rows.Next() {
		var uid, raw, fetched string
		if err = rows.Scan(&uid, &raw, &fetched); err != nil {
			writeError(w, 500, "读取作者资料缓存失败")
			return
		}
		if !wanted[uid] {
			continue
		}
		item, e := cachedProfile(raw, fetched)
		if e != nil {
			writeError(w, 500, "作者资料缓存无法读取")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		writeError(w, 500, "读取作者资料缓存失败")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) authorProfile(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	if !authorUIDPattern.MatchString(uid) || !strings.HasPrefix(uid, "MS4") {
		writeError(w, 400, "作者标识无效，旧记录可继续查看本地作品")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	// Serialize profile refreshes. Recheck the persistent cache after waiting so
	// two tabs requesting the same missing author make one upstream request.
	select {
	case s.profileSlot <- struct{}{}:
		defer func() { <-s.profileSlot }()
	case <-ctx.Done():
		upstreamError(w, ctx.Err())
		return
	}
	cached, err := s.store.CachedAuthor(uid)
	hasCache := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 500, "读取作者资料缓存失败")
		return
	}
	if hasCache && !cached.Stale && r.URL.Query().Get("refresh") != "1" {
		writeJSON(w, 200, cached)
		return
	}
	select {
	case s.parseSlots <- struct{}{}:
		defer func() { <-s.parseSlots }()
	default:
		writeError(w, 429, "正在读取其他数据，请稍后重试作者资料")
		return
	}
	profile, err := s.resolver.ResolveAuthor(ctx, "https://www.douyin.com/user/"+uid)
	if err != nil {
		if !hasCache {
			upstreamError(w, err)
			return
		}
		cached.Stale = true
		cached.Warning = "更新作者资料失败，正在显示已缓存资料"
		var classified *ParseError
		if errors.As(err, &classified) {
			cached.WarningCode = classified.Code
		}
		writeJSON(w, 200, cached)
		return
	}
	if err = s.store.SaveAuthor(profile); err != nil {
		writeError(w, 500, "保存作者资料失败")
		return
	}
	item, err := s.store.CachedAuthor(uid)
	if err != nil {
		writeError(w, 500, "读取作者资料失败")
		return
	}
	writeJSON(w, 200, item)
}

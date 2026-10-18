package app

// Normalization and planning follow src/dtk/models/content.py and
// src/dtk/media/plan.py. Mirrors retain the parser's order; bitrate is not a
// sorting criterion because a larger alternate may carry a watermark.

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

const imageMaxBytes int64 = 32 << 20
const maxMediaItems = 64
const mediaUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

type Image struct {
	URL    string   `json:"url"`
	URLs   []string `json:"urls"`
	Width  *int64   `json:"width"`
	Height *int64   `json:"height"`
}
type Stream struct {
	Image
	Bitrate   *int64  `json:"bitrate"`
	Format    *string `json:"format"`
	SizeBytes *int64  `json:"size_bytes"`
	Watermark bool    `json:"watermark"`
}
type Manifest struct {
	Covers  []Image  `json:"covers"`
	Video   *Stream  `json:"video"`
	Streams []Stream `json:"streams"`
	Images  []Image  `json:"images"`
}
type ContentInfo struct {
	IsDeleted         bool     `json:"is_deleted"`
	IsPrivate         bool     `json:"is_private"`
	Kind              string   `json:"kind"`
	Description       string   `json:"description"`
	AuthorUID         string   `json:"author_uid"`
	PlatformCreatedAt *string  `json:"platform_created_at"`
	Tags              []string `json:"tags"`
	MusicID           string   `json:"music_id"`
	MusicTitle        string   `json:"music_title"`
	Location          string   `json:"location"`
	ImageCount        int      `json:"image_count"`
	FetchedAt         string   `json:"fetched_at"`
}
type ImagePreview struct {
	Index  int    `json:"index"`
	URL    string `json:"url"`
	Width  *int64 `json:"width"`
	Height *int64 `json:"height"`
}
type PlannedItem struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Mirrors  []string `json:"mirrors"`
	Accept   []string `json:"accept"`
	MaxBytes int64    `json:"max_bytes"`
}
type Plan struct {
	Items   []PlannedItem `json:"items"`
	Skipped []string      `json:"skipped"`
}
type StoredFile struct {
	Retained    bool   `json:"retained,omitempty"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	Bytes       int64  `json:"bytes"`
	SHA256      string `json:"sha256,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Error       string `json:"error,omitempty"`
}

func extension(raw string, video bool) string {
	u, _ := url.Parse(raw)
	fallback := "jpg"
	allowed := []string{"jpg", "jpeg", "png", "webp", "heic", "gif"}
	if video {
		fallback = "mp4"
		allowed = []string{"mp4", "mov", "webm", "m4v"}
	}
	if u == nil {
		return fallback
	}
	suffix := strings.ToLower(strings.TrimPrefix(path.Ext(u.Path), "."))
	for _, candidate := range allowed {
		if suffix == candidate {
			return suffix
		}
	}
	return fallback
}
func sourceMirrors(image Image) (out, refused []string) {
	out = []string{}
	refused = []string{}
	seen := map[string]bool{}
	for _, raw := range append([]string{image.URL}, image.URLs...) {
		if raw == "" {
			continue
		}
		normalized := normalizeMediaURL(raw)
		if normalized == "" {
			u, err := url.Parse(raw)
			host := "无效 URL"
			if err == nil {
				host = u.Hostname()
			}
			refused = append(refused, "媒体域名或路径不受支持："+host)
			continue
		}
		if !seen[normalized] {
			seen[normalized] = true
			out = append(out, normalized)
		}
	}
	return
}
func buildPlan(c Content, maxBytes int64, includeCover bool) Plan {
	plan := Plan{Items: []PlannedItem{}, Skipped: []string{}}
	note := func(reasons []string) {
		for _, reason := range reasons {
			found := false
			for _, previous := range plan.Skipped {
				if previous == reason {
					found = true
					break
				}
			}
			if !found {
				plan.Skipped = append(plan.Skipped, reason)
			}
		}
	}
	manifest := c.Manifest
	// Old v1 rows had only one mirror list. Keep them downloadable after migration.
	if manifest.Video == nil && len(manifest.Streams) == 0 && len(manifest.Images) == 0 && len(c.MediaURLs) > 0 {
		manifest.Video = &Stream{Image: Image{URL: c.MediaURLs[0], URLs: c.MediaURLs}}
		if c.Cover != "" {
			manifest.Covers = []Image{{URL: c.Cover, URLs: []string{c.Cover}}}
		}
	}
	streams := []Stream{}
	if manifest.Video != nil {
		streams = append(streams, *manifest.Video)
	}
	streams = append(streams, manifest.Streams...)
	if len(streams) > 0 {
		mirrors := []string{}
		seen := map[string]bool{}
		for _, stream := range streams {
			urls, reasons := sourceMirrors(stream.Image)
			note(reasons)
			for _, raw := range urls {
				if !seen[raw] {
					seen[raw] = true
					mirrors = append(mirrors, raw)
				}
			}
		}
		if len(mirrors) > 0 {
			plan.Items = append(plan.Items, PlannedItem{"video." + extension(mirrors[0], true), "video", mirrors, []string{"video/", "application/octet-stream"}, maxBytes})
		} else {
			note([]string{"no video mirror survived the media allowlist"})
		}
	}
	for i, image := range manifest.Images {
		if len(plan.Items) >= maxMediaItems {
			break
		}
		mirrors, reasons := sourceMirrors(image)
		note(reasons)
		if len(mirrors) == 0 {
			continue
		}
		plan.Items = append(plan.Items, PlannedItem{fmt.Sprintf("image-%02d.%s", i+1, extension(mirrors[0], false)), "image", mirrors, []string{"image/"}, min(maxBytes, imageMaxBytes)})
	}
	if includeCover && len(plan.Items) < maxMediaItems {
		for _, image := range manifest.Covers {
			mirrors, reasons := sourceMirrors(image)
			note(reasons)
			if len(mirrors) == 0 {
				continue
			}
			plan.Items = append(plan.Items, PlannedItem{"cover." + extension(mirrors[0], false), "cover", mirrors, []string{"image/"}, min(maxBytes, imageMaxBytes)})
			break
		}
	}
	if len(plan.Items) == 0 && len(plan.Skipped) == 0 {
		plan.Skipped = append(plan.Skipped, "this post has no downloadable media")
	}
	return plan
}
func exportName(contentID, name string) string { return "dtk-douyin-" + contentID + "-" + name }
func sidecar(c Content, plan Plan) map[string]any {
	files := make([]map[string]string, 0, len(plan.Items))
	for _, item := range plan.Items {
		files = append(files, map[string]string{"name": item.Name, "kind": item.Kind})
	}
	var duration any
	if c.Duration > 0 {
		duration = c.Duration
	}
	return map[string]any{"platform": "douyin", "content_id": c.ID, "kind": c.Kind, "web_url": c.SourceURL, "title": c.Title, "description": c.Description, "created_at": c.PlatformCreatedAt, "duration_ms": duration, "author": map[string]string{"uid": c.AuthorUID, "nickname": c.Author}, "music": map[string]any{"music_id": optionalText(c.MusicID), "title": optionalText(c.MusicTitle)}, "tags": c.Tags, "location": optionalText(c.Location), "files": files, "archived_at": c.FetchedAt, "written_by": "dtk"}
}

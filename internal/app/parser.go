package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var linkPattern = regexp.MustCompile(`(?i)https?://[^\s<>"'` + "`" + `\\\x{2018}\x{2019}\x{201c}\x{201d}\x{3000}-\x{303f}\x{3400}-\x{4dbf}\x{4e00}-\x{9fff}\x{ff00}-\x{ffef}\x{1f000}-\x{1faff}]+|(?:v\.douyin\.com|v\.amemv\.com|www\.douyin\.com|douyin\.com|m\.douyin\.com|www\.iesdouyin\.com|iesdouyin\.com)/[^\s<>"'` + "`" + `\\\x{2018}\x{2019}\x{201c}\x{201d}\x{3000}-\x{303f}\x{3400}-\x{4dbf}\x{4e00}-\x{9fff}\x{ff00}-\x{ffef}\x{1f000}-\x{1faff}]*`)
var videoPattern = regexp.MustCompile(`^/(?:share/)?(?:video|note|slides)/(\d+)`)
var idPattern = regexp.MustCompile(`^\d{1,20}$`)
var scriptPattern = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
var renderID = regexp.MustCompile(`(?i)\bid\s*=\s*["'](?:RENDER_DATA|__NEXT_DATA__)["']`)
var errVideoDataMissing = errors.New("页面中没有匹配的作品数据")
var errNoMedia = errors.New("作品没有可下载的媒体地址")

type ParseError struct {
	Code    string
	Message string
}

func (e *ParseError) Error() string { return e.Message }

func extractLink(input string) (string, error) {
	for _, at := range linkPattern.FindAllStringIndex(input, -1) {
		match := input[at[0]:at[1]]
		if !strings.Contains(match, "://") && at[0] > 0 {
			before := input[at[0]-1]
			if before >= 'a' && before <= 'z' || before >= 'A' && before <= 'Z' || before >= '0' && before <= '9' || strings.ContainsRune("._-/@", rune(before)) {
				continue
			}
		}
		raw := strings.TrimRight(match, ".,;:!?)]}>'\"、。，！？）】》”’")
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		if allowedURL(raw, pageDomains) {
			return raw, nil
		}
	}
	return "", &ParseError{"INVALID_URL", "请粘贴抖音作品分享链接，支持视频、图集和完整分享文案"}
}

func contentID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	// Match the reference route precedence: a modal on a profile is still a post.
	for _, key := range []string{"modal_id", "vid"} {
		v := u.Query().Get(key)
		if idPattern.MatchString(v) {
			return v
		}
	}
	if m := videoPattern.FindStringSubmatch(u.Path); len(m) > 1 {
		return m[1]
	}
	// Retain the original personal edition's explicit API-id compatibility.
	for _, key := range []string{"aweme_id", "item_ids"} {
		v := u.Query().Get(key)
		if idPattern.MatchString(v) {
			return v
		}
	}
	return ""
}
func validContentID(id string) bool {
	if !idPattern.MatchString(id) {
		return false
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 {
		return false
	}
	stamp := int64(n >> 32)
	return stamp >= 1451606400 && stamp <= time.Now().Add(10*time.Minute).Unix()
}

type Resolver struct {
	client *http.Client
	store  *Store
}

func NewResolver(s *Store) *Resolver { return &Resolver{newHTTPClient(pageDomains, 20*time.Second), s} }

func (r *Resolver) Resolve(ctx context.Context, input string) (Content, error) {
	link, err := extractLink(input)
	if err != nil {
		return Content{}, err
	}
	cookie, err := r.store.Cookie()
	if err != nil {
		return Content{}, fmt.Errorf("读取 Cookie 配置失败")
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	id := contentID(link)
	if id == "" {
		link, err = r.expand(ctx, link)
		if err != nil {
			return Content{}, err
		}
		id = contentID(link)
	}
	if !validContentID(id) {
		return Content{}, &ParseError{"INVALID_PARAM", "链接没有包含有效的抖音作品 ID，请复制单个视频或图集的分享链接"}
	}
	var failures []error
	// Use the same web detail API and signing protocol as the reference service.
	// The share-page route remains an explicit compatibility fallback for personal use.
	if cookie != "" {
		if c, e := r.detail(ctx, id, cookie); e == nil {
			return c, nil
		} else {
			failures = append(failures, e)
		}
	}
	hint := "video"
	u, _ := url.Parse(link)
	if u != nil && (strings.Contains(u.Path, "/note/") || strings.Contains(u.Path, "/slides/")) {
		hint = "note"
	}
	targets := []string{"https://www.iesdouyin.com/share/" + hint + "/" + id + "/", "https://www.douyin.com/" + hint + "/" + id}
	if hint == "note" {
		targets = append(targets, "https://www.iesdouyin.com/share/video/"+id+"/")
	}
	for _, target := range targets {
		if ctx.Err() != nil {
			return Content{}, ctx.Err()
		}
		body, _, e := r.page(ctx, target, cookie)
		if e != nil {
			failures = append(failures, e)
			continue
		}
		if c, e := parsePage(body, id); e == nil {
			return c, nil
		} else {
			failures = append(failures, e)
		}
	}
	// Prefer an actionable classified response over a generic missing-script error.
	for _, e := range failures {
		var classified *ParseError
		if errors.As(e, &classified) {
			return Content{}, classified
		}
	}
	for _, e := range failures {
		if errors.Is(e, errNoMedia) {
			return Content{}, e
		}
	}
	return Content{}, &ParseError{"UPSTREAM_RISK_CONTROL", "未取得作品数据：抖音可能要求验证或当前页面没有媒体数据。请确认原作品可播放并更新完整 Cookie 后重试"}
}
func (r *Resolver) expand(ctx context.Context, raw string) (string, error) {
	client := *r.client
	client.Timeout = 10 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	seen := map[string]bool{}
	for hop := 0; hop < 8; hop++ {
		if !allowedURL(raw, pageDomains) {
			return "", &ParseError{"INVALID_URL", "短链接跳转到了不支持的域名"}
		}
		if contentID(raw) != "" || authorUID(raw) != "" {
			return raw, nil
		}
		if seen[raw] {
			return "", &ParseError{"INVALID_URL", "短链接发生循环跳转"}
		}
		seen[raw] = true
		headers := http.Header{}
		headers.Set("User-Agent", mediaUserAgent)
		resp, err := request(ctx, &client, raw, "", pageDomains, headers)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("展开抖音短链接失败，请检查网络")
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode != 301 && resp.StatusCode != 302 && resp.StatusCode != 303 && resp.StatusCode != 307 && resp.StatusCode != 308 {
			return raw, nil
		}
		location, err := resp.Location()
		if err != nil {
			return "", fmt.Errorf("短链接返回了无效的跳转地址")
		}
		raw = location.String()
	}
	return "", fmt.Errorf("短链接跳转次数过多")
}
func (r *Resolver) detail(ctx context.Context, id, cookie string) (Content, error) {
	target, headers := signedDetailURL(id, cookie, time.Now().UnixMilli())
	extra := http.Header{}
	extra.Set("User-Agent", desktopUA)
	for k, v := range headers {
		extra.Set(k, v)
	}
	resp, err := request(ctx, r.client, target, cookie, pageDomains, extra)
	if err != nil {
		if ctx.Err() != nil {
			return Content{}, ctx.Err()
		}
		return Content{}, fmt.Errorf("连接抖音作品详情接口失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 {
		return Content{}, &ParseError{"UPSTREAM_RISK_CONTROL", fmt.Sprintf("抖音作品接口返回 HTTP %d，请更新完整 Cookie 或稍后重试", resp.StatusCode)}
	}
	if resp.StatusCode != 200 {
		return Content{}, fmt.Errorf("抖音作品接口返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	if err != nil || len(body) > 8<<20 {
		return Content{}, fmt.Errorf("抖音作品数据读取失败或过大")
	}
	return parsePage(body, id)
}
func (r *Resolver) page(ctx context.Context, raw, cookie string) ([]byte, string, error) {
	resp, err := request(ctx, r.client, raw, cookie, pageDomains)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fmt.Errorf("连接抖音分享页失败，请检查网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("抖音分享页返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	if err != nil || len(body) > 8<<20 {
		return nil, "", fmt.Errorf("抖音页面读取失败或过大")
	}
	return body, resp.Request.URL.String(), nil
}

func parsePage(body []byte, id string) (Content, error) {
	var last error = errVideoDataMissing
	parse := func(payload string) (Content, error) {
		var data any
		dec := json.NewDecoder(strings.NewReader(payload))
		dec.UseNumber()
		if dec.Decode(&data) != nil {
			return Content{}, errVideoDataMissing
		}
		if root := object(data); root != nil {
			if err := riskControl(root); err != nil {
				return Content{}, err
			}
		}
		if root := object(data); root != nil {
			if detail, exists := root["aweme_detail"]; exists {
				item := object(detail)
				if textValue(item["aweme_id"]) != id {
					return Content{}, errVideoDataMissing
				}
				if err := validateDetail(item); err != nil {
					return Content{}, err
				}
				return parseVideo(item, id)
			}
		}
		for _, item := range findContents(data, id) {
			c, err := parseVideo(item, id)
			if err == nil {
				return c, nil
			}
			last = err
		}
		return Content{}, last
	}
	for _, script := range scriptPattern.FindAllSubmatch(body, -1) {
		payload := strings.TrimSpace(string(script[2]))
		if renderID.Match(script[1]) {
			payload = html.UnescapeString(payload)
			if strings.HasPrefix(payload, "%") {
				if decoded, err := url.PathUnescape(payload); err == nil {
					payload = decoded
				}
			}
		} else if at := strings.Index(payload, "_ROUTER_DATA"); at >= 0 {
			if eq := strings.Index(payload[at:], "="); eq >= 0 {
				payload = payload[at+eq+1:]
			} else {
				continue
			}
		} else {
			continue
		}
		if c, err := parse(payload); err == nil {
			return c, nil
		} else if !errors.Is(err, errVideoDataMissing) {
			last = err
		}
	}
	if c, err := parse(string(body)); err == nil {
		return c, nil
	} else if !errors.Is(err, errVideoDataMissing) {
		last = err
	}
	return Content{}, last
}
func findContents(data any, id string) []map[string]any {
	var out []map[string]any
	switch value := data.(type) {
	case map[string]any:
		if textValue(value["aweme_id"]) == id {
			if _, ok := value["video"]; ok {
				out = append(out, value)
			} else if _, ok := value["images"]; ok {
				out = append(out, value)
			}
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out = append(out, findContents(value[key], id)...)
		}
	case []any:
		for _, child := range value {
			out = append(out, findContents(child, id)...)
		}
	}
	return out
}
func findVideo(data any, id string) map[string]any {
	if found := findContents(data, id); len(found) > 0 {
		return found[0]
	}
	return nil
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func textValue(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	}
	return ""
}
func nullableInt(v any) *int64 {
	if v == nil {
		return nil
	}
	if _, ok := v.(bool); ok {
		return nil
	}
	text := textValue(v)
	if text == "" {
		return nil
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		f, e := strconv.ParseFloat(text, 64)
		if e != nil {
			return nil
		}
		n = int64(f)
	}
	return &n
}
func number(v any) int64 {
	if n := nullableInt(v); n != nil {
		return *n
	}
	return 0
}
func boolValue(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no", "":
			return false
		}
	default:
		return number(v) != 0
	}
	return false
}
func optionalText(v any) *string {
	s := textValue(v)
	if s == "" {
		return nil
	}
	return &s
}
func cleanURLs(v any) []string {
	out := []string{}
	seen := map[string]bool{}
	list, _ := v.([]any)
	for _, entry := range list {
		raw, ok := entry.(string)
		if !ok {
			continue
		}
		raw = strings.TrimSpace(raw)
		if (!strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "http://")) || seen[raw] {
			continue
		}
		seen[raw] = true
		out = append(out, raw)
	}
	return out
}
func imageFromNode(v any) *Image {
	m := object(v)
	if m == nil {
		return nil
	}
	urls := cleanURLs(m["url_list"])
	if len(urls) == 0 {
		return nil
	}
	return &Image{urls[0], urls, nullableInt(m["width"]), nullableInt(m["height"])}
}
func streamFromNode(v any, fallback map[string]any) *Stream {
	image := imageFromNode(v)
	if image == nil {
		return nil
	}
	m := object(v)
	if image.Width == nil || *image.Width == 0 {
		image.Width = nullableInt(fallback["width"])
	}
	if image.Height == nil || *image.Height == 0 {
		image.Height = nullableInt(fallback["height"])
	}
	format := optionalText(m["format"])
	if format == nil {
		format = optionalText(fallback["format"])
	}
	return &Stream{Image: *image, Format: format, SizeBytes: nullableInt(m["data_size"])}
}
func mediaURLs(v any) []string {
	out := []string{}
	for _, raw := range cleanURLs(object(v)["url_list"]) {
		if normalized := normalizeMediaURL(raw); normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

// Required fields of the reference web detail contract. The separately
// supported mobile share page intentionally has abbreviated metadata.
func validateDetail(item map[string]any) error {
	required := []string{"aweme_id", "desc", "status", "author", "statistics"}
	for _, key := range required {
		value, exists := item[key]
		if !exists || value == nil {
			return &ParseError{"UPSTREAM_CHANGED", "作品数据缺少 aweme_detail." + key}
		}
	}
	for _, key := range []string{"status", "author", "statistics"} {
		if object(item[key]) == nil {
			return &ParseError{"UPSTREAM_CHANGED", "作品字段类型变化：aweme_detail." + key}
		}
	}
	author := object(item["author"])
	for _, key := range []string{"sec_uid", "nickname"} {
		value, exists := author[key]
		if !exists || value == nil {
			return &ParseError{"UPSTREAM_CHANGED", "作品数据缺少 aweme_detail.author." + key}
		}
	}
	for _, entry := range []struct {
		value any
		path  string
	}{{item["desc"], "desc"}, {author["nickname"], "author.nickname"}} {
		if _, ok := entry.value.(string); !ok {
			return &ParseError{"UPSTREAM_CHANGED", "作品字段类型变化：aweme_detail." + entry.path}
		}
	}
	if textValue(author["sec_uid"]) == "" {
		return &ParseError{"UPSTREAM_CHANGED", "作品作者缺少 sec_uid"}
	}
	return nil
}
func truthy(value any) bool {
	if value == nil {
		return false
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Map, reflect.Slice, reflect.String:
		return rv.Len() > 0
	}
	return !rv.IsZero()
}
func riskControl(root map[string]any) error {
	code := number(root["status_code"])
	blocked := len(root) == 0 || code == 8 || code == 2154 || code == 2156 || code == 10000 || code == 10001
	for _, key := range []string{"verify_center_decision_conf", "captcha", "verify_page"} {
		if value, ok := root[key]; ok && truthy(value) {
			blocked = true
		}
	}
	for _, key := range []string{"aweme_detail", "user"} {
		if value, ok := root[key]; ok && !truthy(value) {
			blocked = true
		}
	}
	if blocked {
		return &ParseError{"UPSTREAM_RISK_CONTROL", "抖音返回了验证或空数据，请确认原作品可访问并更新 Cookie 后重试"}
	}
	return nil
}
func parseVideo(item map[string]any, id string) (Content, error) {
	status := object(item["status"])
	withdrawn := boolValue(status["is_delete"]) || number(status["private_status"]) != 0
	video := object(item["video"])
	author := object(item["author"])
	description := textValue(item["desc"])
	title := ""
	for _, line := range regexp.MustCompile(`[\r\n\v\f\x{0085}\x{2028}\x{2029}]`).Split(description, -1) {
		if title = strings.TrimSpace(line); title != "" {
			break
		}
	}
	deleted := boolValue(status["is_delete"])
	c := Content{ID: id, Title: title, Author: textValue(author["nickname"]), ContentInfo: ContentInfo{IsDeleted: deleted, IsPrivate: number(status["private_status"]) != 0, Kind: "video", Description: description, AuthorUID: textValue(author["sec_uid"]), Tags: []string{}, FetchedAt: now()}, Manifest: Manifest{Covers: []Image{}, Streams: []Stream{}, Images: []Image{}}}
	// Mobile share pages abbreviate author metadata; full web API responses use sec_uid.
	if c.AuthorUID == "" {
		c.AuthorUID = textValue(author["uid"])
	}
	if stamp := number(item["create_time"]); stamp > 0 {
		nanos := int64(0)
		if stamp > 10000000000 {
			nanos = (stamp % 1000) * int64(time.Millisecond)
			stamp /= 1000
		}
		value := time.Unix(stamp, nanos).UTC().Format(time.RFC3339Nano)
		c.PlatformCreatedAt = &value
	}
	music := object(item["music"])
	c.MusicID = textValue(music["id_str"])
	if c.MusicID == "" {
		c.MusicID = textValue(music["id"])
	}
	c.MusicTitle = textValue(music["title"])
	c.Location = textValue(object(item["poi_info"])["poi_name"])
	extras, _ := item["text_extra"].([]any)
	for _, extra := range extras {
		if tag := textValue(object(extra)["hashtag_name"]); tag != "" {
			duplicate := false
			for _, previous := range c.Tags {
				if previous == tag {
					duplicate = true
					break
				}
			}
			if !duplicate {
				c.Tags = append(c.Tags, tag)
			}
		}
	}
	for _, key := range []string{"cover", "origin_cover", "dynamic_cover"} {
		if image := imageFromNode(video[key]); image != nil {
			duplicate := false
			for _, old := range c.Manifest.Covers {
				if reflect.DeepEqual(old, *image) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				c.Manifest.Covers = append(c.Manifest.Covers, *image)
			}
		}
	}
	rawImages, _ := item["images"].([]any)
	images := make([]any, 0, len(rawImages))
	for _, entry := range rawImages {
		if object(entry) != nil {
			images = append(images, entry)
		}
	}
	if len(images) > 0 {
		c.Kind = "image_album"
		c.ImageCount = len(images)
		for i, entry := range images {
			image := imageFromNode(entry)
			if image == nil {
				return Content{}, &ParseError{"UPSTREAM_CHANGED", fmt.Sprintf("图集第 %d 张图片缺少 url_list，平台数据结构可能变化", i+1)}
			}
			c.Manifest.Images = append(c.Manifest.Images, *image)
		}
	} else {
		c.Duration = number(video["duration"])
		if c.Duration <= 0 {
			c.Duration = number(item["duration"])
		}
		if c.Duration < 0 {
			c.Duration = 0
		}
		c.Manifest.Video = streamFromNode(video["play_addr"], video)
		// The reference requires a primary play_addr even if alternates exist.
		if c.Manifest.Video == nil && !withdrawn {
			return Content{}, &ParseError{"UPSTREAM_CHANGED", "作品数据缺少 video.play_addr，平台结构可能已变化"}
		}
		rates, _ := video["bit_rate"].([]any)
		for _, gear := range rates {
			entry := object(gear)
			if stream := streamFromNode(entry["play_addr"], video); stream != nil {
				stream.Bitrate = nullableInt(entry["bit_rate"])
				if format := optionalText(entry["format"]); format != nil {
					stream.Format = format
				}
				c.Manifest.Streams = append(c.Manifest.Streams, *stream)
			}
		}
		if stream := streamFromNode(video["download_addr"], video); stream != nil {
			stream.Watermark = true
			c.Manifest.Streams = append(c.Manifest.Streams, *stream)
		}
	}
	c.Width = int(number(video["width"]))
	c.Height = int(number(video["height"]))
	c.SourceURL = "https://www.douyin.com/video/" + id
	if c.Kind == "image_album" {
		c.SourceURL = "https://www.douyin.com/note/" + id
	}
	if len(c.Manifest.Covers) > 0 {
		c.Cover = c.Manifest.Covers[0].URL
	} else if len(c.Manifest.Images) > 0 {
		c.Cover = c.Manifest.Images[0].URL
	}
	plan := buildPlan(c, maxVideoBytes, true)
	if len(plan.Items) == 0 && !withdrawn {
		return Content{}, fmt.Errorf("%w：%s", errNoMedia, strings.Join(plan.Skipped, "；"))
	}
	for _, item := range plan.Items {
		if item.Kind == "video" {
			c.MediaURLs = append(c.MediaURLs, item.Mirrors...)
			break
		}
	}
	return c, nil
}

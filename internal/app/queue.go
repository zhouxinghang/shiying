package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxVideoBytes int64 = 512 << 20
const mirrorMaxAge = 600 * time.Second

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

type activeJob struct {
	cancel  context.CancelFunc
	attempt int
}
type Queue struct {
	store    *Store
	resolver *Resolver
	client   *http.Client
	dir      string
	mu       sync.Mutex
	active   map[string]activeJob
	wg       sync.WaitGroup
	cancel   context.CancelFunc
}

func NewQueue(store *Store, resolver *Resolver, dir string) (*Queue, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	client := newHTTPClient(mediaDomains, 15*time.Minute)
	policy := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("媒体跳转次数过多")
		}
		return policy(req, via)
	}
	q := &Queue{store: store, resolver: resolver, client: client, dir: dir, active: map[string]activeJob{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".previous") {
			dest := filepath.Join(dir, strings.TrimSuffix(e.Name(), ".previous"))
			if _, statErr := os.Stat(dest); os.IsNotExist(statErr) {
				if err := os.Rename(filepath.Join(dir, e.Name()), dest); err != nil {
					return nil, err
				}
				continue
			}
		}
		if strings.HasSuffix(e.Name(), ".part") || strings.HasSuffix(e.Name(), ".previous") {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return nil, err
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	q.cancel = cancel
	for i := 0; i < 2; i++ {
		q.wg.Add(1)
		go q.worker(ctx)
	}
	return q, nil
}
func (q *Queue) Close() {
	q.cancel()
	q.wg.Wait()
	q.client.CloseIdleConnections()
	q.resolver.client.CloseIdleConnections()
}
func (q *Queue) taskDir(id string) string { return filepath.Join(q.dir, id) }
func (q *Queue) file(id string) string {
	if t, err := q.store.Task(id); err == nil {
		for _, f := range t.Files {
			if f.Kind == "video" && f.State == "done" {
				return filepath.Join(q.taskDir(id), f.Name)
			}
		}
	}
	return filepath.Join(q.dir, id+".mp4")
}
func (q *Queue) worker(ctx context.Context) {
	defer q.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		t, err := q.store.Claim()
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				slog.Error("claim download", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				continue
			}
		}
		q.run(ctx, t)
	}
}
func (q *Queue) run(parent context.Context, t Task) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	defer cancel()
	q.mu.Lock()
	current, err := q.store.Task(t.ID)
	if err != nil || current.Status != "downloading" || current.Attempt != t.Attempt {
		q.mu.Unlock()
		return
	}
	q.active[t.ID] = activeJob{cancel, t.Attempt}
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		if j, ok := q.active[t.ID]; ok && j.attempt == t.Attempt {
			delete(q.active, t.ID)
		}
		q.mu.Unlock()
	}()
	previousFiles := append([]StoredFile(nil), t.Files...)
	previousKind := t.Kind
	plan := buildPlan(t.Video, maxVideoBytes, true)
	fetched, e := time.Parse(time.RFC3339Nano, t.FetchedAt)
	stale := e != nil || time.Since(fetched) > mirrorMaxAge
	// Existing v1 tasks can finish their first known mirror; retries and recovered
	// tasks always refresh. A stale manifest remains usable if refreshing fails,
	// just as worker/ops/media_download.py does in the reference.
	legacyFirst := t.FetchedAt == "" && len(t.MediaURLs) > 0 && t.Attempt == 1
	if len(plan.Items) == 0 || t.Attempt > 1 || stale && !legacyFirst {
		c, refreshErr := q.resolver.Resolve(ctx, t.SourceURL)
		if refreshErr == nil {
			t.Video = c
			plan = buildPlan(c, maxVideoBytes, true)
			if err = q.store.SaveContent(t); err != nil {
				q.fail(t, fmt.Errorf("保存作品信息失败"))
				return
			}
		} else if ctx.Err() != nil || len(plan.Items) == 0 {
			q.interrupted(parent, t, refreshErr)
			return
		}
	}
	if len(plan.Items) == 0 {
		q.fail(t, errNoMedia)
		return
	}
	stage := filepath.Join(q.dir, "."+t.ID+"."+strconv.Itoa(t.Attempt)+".part")
	if err = os.MkdirAll(stage, 0700); err != nil {
		q.fail(t, fmt.Errorf("创建作品目录失败"))
		return
	}
	defer os.RemoveAll(stage)
	meta, _ := json.MarshalIndent(sidecar(t.Video, plan), "", "  ")
	if err = os.WriteFile(filepath.Join(stage, "meta.json"), meta, 0600); err != nil {
		q.fail(t, fmt.Errorf("保存元数据失败"))
		return
	}
	files := make([]StoredFile, len(plan.Items))
	amounts := make([]int64, len(files))
	totals := make([]int64, len(files))
	for i, item := range plan.Items {
		files[i] = StoredFile{Name: item.Name, Kind: item.Kind, State: "queued"}
	}
	var progressMu sync.Mutex
	last := time.Time{}
	save := func() {
		var n, total int64
		for i := range amounts {
			n += amounts[i]
			total += totals[i]
		}
		if err := q.store.SaveFiles(t, files, n, total); err != nil {
			slog.Error("save file progress", "id", t.ID, "error", err)
		}
	}
	save()
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, item := range plan.Items {
		wg.Add(1)
		go func(i int, item PlannedItem) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			progressMu.Lock()
			files[i].State = "downloading"
			progressMu.Unlock()
			result, transferErr := q.fetch(ctx, item, filepath.Join(stage, item.Name), func(n, total int64) {
				progressMu.Lock()
				defer progressMu.Unlock()
				amounts[i] = n
				totals[i] = total
				if time.Since(last) > 500*time.Millisecond {
					save()
					last = time.Now()
				}
			})
			progressMu.Lock()
			defer progressMu.Unlock()
			if transferErr != nil {
				files[i].State = "failed"
				files[i].Error = transferErr.Error()
				amounts[i] = 0
				totals[i] = 0
			} else {
				files[i] = result
				amounts[i] = result.Bytes
				totals[i] = result.Bytes
			}
			save()
		}(i, item)
	}
	wg.Wait()
	if ctx.Err() != nil {
		q.interrupted(parent, t, ctx.Err())
		return
	}
	// A failed retry must not destroy already archived slides. Reuse a
	// matching previously verified file, but keep the task partial and report
	// that this attempt failed to refresh it. Hard links avoid a second copy.
	retained := 0
	q.mu.Lock()
	current, err = q.store.Task(t.ID)
	if err != nil || current.Status != "downloading" || current.Attempt != t.Attempt {
		q.mu.Unlock()
		return
	}
	if previousKind == t.Kind {
		for i, file := range files {
			if file.State == "done" {
				continue
			}
			for _, old := range previousFiles {
				if old.Name != file.Name || old.Kind != file.Kind || old.State != "done" || old.Name != filepath.Base(old.Name) {
					continue
				}
				source := filepath.Join(q.taskDir(t.ID), old.Name)
				info, e := os.Stat(source)
				if e != nil || !info.Mode().IsRegular() || info.Size() != old.Bytes {
					continue
				}
				if e = os.Link(source, filepath.Join(stage, old.Name)); e != nil {
					continue
				}
				old.Retained = true
				old.Error = "本次未重新取得，保留上次保存的文件：" + file.Error
				files[i] = old
				amounts[i] = old.Bytes
				totals[i] = old.Bytes
				retained++
				break
			}
		}
		save()
	}
	q.mu.Unlock()
	done := 0
	var failures []string
	for _, f := range files {
		if f.State == "done" {
			done++
			if f.Retained {
				failures = append(failures, f.Name+"："+f.Error)
			}
		} else {
			failures = append(failures, f.Name+"："+f.Error)
		}
	}
	if done == 0 {
		q.fail(t, fmt.Errorf("%s", strings.Join(failures, "；")))
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	current, err = q.store.Task(t.ID)
	if err != nil || current.Status != "downloading" || current.Attempt != t.Attempt {
		return
	}
	dest := q.taskDir(t.ID)
	previous := dest + ".previous"
	if _, err = os.Stat(dest); err == nil {
		if err = os.Rename(dest, previous); err != nil {
			q.fail(t, fmt.Errorf("替换作品目录失败"))
			return
		}
	}
	if err = os.Rename(stage, dest); err != nil {
		_ = os.Rename(previous, dest)
		q.fail(t, fmt.Errorf("保存作品失败"))
		return
	}
	_ = os.RemoveAll(previous)
	status, message := "completed", ""
	if done < len(files) || retained > 0 {
		status = "partial"
		message = fmt.Sprintf("已保存 %d/%d 个文件。%s", done, len(files), strings.Join(failures, "；"))
	}
	if err = q.store.Finish(t, status, message); err != nil {
		slog.Error("complete download", "id", t.ID, "error", err)
	}
}
func (q *Queue) interrupted(parent context.Context, t Task, err error) {
	if parent.Err() != nil {
		_, e := q.store.db.Exec(`UPDATE tasks SET status='queued',downloaded=0,total=0,media_urls='[]',content_json=json_set(content_json,'$.fetched_at',''),updated_at=? WHERE id=? AND attempt=? AND status='downloading'`, now(), t.ID, t.Attempt)
		if e != nil {
			slog.Error("requeue interrupted download", "error", e)
		}
		return
	}
	q.fail(t, err)
}
func (q *Queue) fail(t Task, err error) {
	message := err.Error()
	if errors.Is(err, context.Canceled) {
		message = "任务已中断，可重试"
	} else if errors.Is(err, context.DeadlineExceeded) {
		message = "下载超时，请重试"
	}
	if e := q.store.Finish(t, "failed", message); e != nil {
		slog.Error("save download failure", "error", e)
	}
}

// Same transfer contract as docker/downloader/download.go: mirrors in order,
// MIME prefix, actual byte ceiling, SHA-256, sync, then an atomic rename.
func (q *Queue) fetch(ctx context.Context, item PlannedItem, dest string, progress func(int64, int64)) (StoredFile, error) {
	var last error = fmt.Errorf("没有可用镜像")
	for _, raw := range item.Mirrors {
		if ctx.Err() != nil {
			return StoredFile{}, ctx.Err()
		}
		result, err := q.fetchOne(ctx, item, raw, dest, progress)
		if err == nil {
			return result, nil
		}
		last = err
	}
	return StoredFile{}, last
}
func (q *Queue) fetchOne(ctx context.Context, item PlannedItem, raw, dest string, progress func(int64, int64)) (StoredFile, error) {
	headers := http.Header{"User-Agent": {mediaUserAgent}, "Accept": {"*/*"}, "Accept-Encoding": {"identity"}}
	resp, err := request(ctx, q.client, raw, "", mediaDomains, headers)
	if err != nil {
		if ctx.Err() != nil {
			return StoredFile{}, ctx.Err()
		}
		return StoredFile{}, fmt.Errorf("连接媒体源失败，请重试以刷新地址")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		logUpstreamHTTPFailure(resp)
		return StoredFile{}, fmt.Errorf("媒体源返回 HTTP %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	accepted := false
	for _, prefix := range item.Accept {
		if strings.HasPrefix(strings.ToLower(ct), prefix) {
			accepted = true
			break
		}
	}
	if !accepted {
		logUpstreamFailure(resp, "content_type", nil, fmt.Errorf("媒体类型与 %s 不匹配", item.Kind))
		label := "图片"
		if item.Kind == "video" {
			label = "视频"
		}
		return StoredFile{}, fmt.Errorf("媒体源返回了非%s内容", label)
	}
	if resp.ContentLength > item.MaxBytes {
		logUpstreamFailure(resp, "response_too_large", nil, fmt.Errorf("文件超过 %d 字节下载上限", item.MaxBytes))
		return StoredFile{}, fmt.Errorf("文件超过 %d 字节下载上限", item.MaxBytes)
	}
	partial := dest + ".part"
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return StoredFile{}, fmt.Errorf("创建媒体文件失败")
	}
	committed := false
	defer func() {
		_ = f.Close()
		if !committed {
			_ = os.Remove(partial)
		}
	}()
	digest := sha256.New()
	reader := io.LimitReader(resp.Body, item.MaxBytes+1)
	var written int64
	buffer := make([]byte, 128<<10)
	for {
		if ctx.Err() != nil {
			return StoredFile{}, ctx.Err()
		}
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if _, err = io.MultiWriter(f, digest).Write(buffer[:n]); err != nil {
				return StoredFile{}, fmt.Errorf("写入磁盘失败")
			}
			written += int64(n)
			if written > item.MaxBytes {
				logUpstreamFailure(resp, "response_too_large", nil, fmt.Errorf("文件超过 %d 字节下载上限", item.MaxBytes))
				return StoredFile{}, fmt.Errorf("文件超过 %d 字节下载上限", item.MaxBytes)
			}
			if progress != nil {
				progress(written, max(resp.ContentLength, 0))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			logUpstreamFailure(resp, "read_body", nil, readErr)
			return StoredFile{}, fmt.Errorf("媒体传输中断，请重试")
		}
	}
	if written == 0 {
		logUpstreamFailure(resp, "read_body", nil, fmt.Errorf("媒体内容为空"))
		return StoredFile{}, fmt.Errorf("媒体内容为空")
	}
	if err = f.Sync(); err != nil {
		return StoredFile{}, fmt.Errorf("同步媒体文件失败")
	}
	if err = f.Close(); err != nil {
		return StoredFile{}, fmt.Errorf("保存媒体文件失败")
	}
	if err = os.Rename(partial, dest); err != nil {
		return StoredFile{}, fmt.Errorf("提交媒体文件失败")
	}
	committed = true
	return StoredFile{Name: item.Name, Kind: item.Kind, State: "done", Bytes: written, SHA256: hex.EncodeToString(digest.Sum(nil)), ContentType: ct}, nil
}
func (q *Queue) Cancel(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, err := q.store.Task(id)
	if err != nil {
		return err
	}
	if t.Status != "queued" && t.Status != "downloading" {
		return fmt.Errorf("当前任务无法取消")
	}
	_, err = q.store.db.Exec("UPDATE tasks SET status='canceled',updated_at=? WHERE id=?", now(), id)
	if j, ok := q.active[id]; ok {
		j.cancel()
	}
	return err
}
func (q *Queue) Retry(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, err := q.store.Task(id)
	if err != nil {
		return err
	}
	if t.Status != "failed" && t.Status != "canceled" && t.Status != "partial" {
		return fmt.Errorf("只有失败、部分完成或已取消的任务可以重试")
	}
	_, err = q.store.db.Exec("UPDATE tasks SET status='queued',downloaded=0,total=0,error='',media_urls='[]',updated_at=? WHERE id=?", now(), id)
	return err
}
func (q *Queue) Delete(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, err := q.store.Task(id); err != nil {
		return err
	}
	if j, ok := q.active[id]; ok {
		j.cancel()
	}
	if err := os.RemoveAll(q.taskDir(id)); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(q.dir, id+".mp4")); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err := q.store.db.Exec("DELETE FROM tasks WHERE id=?", id)
	return err
}

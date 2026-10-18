package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Content struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Author    string   `json:"author"`
	Cover     string   `json:"cover"`
	Duration  int64    `json:"duration"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	SourceURL string   `json:"source_url"`
	MediaURLs []string `json:"-"`
	ContentInfo
	Manifest Manifest       `json:"-"`
	Images   []ImagePreview `json:"images,omitempty"`
}

// Compatibility with the initial API and its fixture tests.
type Video = Content

type Task struct {
	ID string `json:"id"`
	Video
	Status         string       `json:"status"`
	Downloaded     int64        `json:"downloaded"`
	Total          int64        `json:"total"`
	Error          string       `json:"error"`
	Favorite       bool         `json:"favorite"`
	CreatedAt      string       `json:"created_at"`
	UpdatedAt      string       `json:"updated_at"`
	Attempt        int          `json:"-"`
	Files          []StoredFile `json:"files"`
	FilesCompleted int          `json:"files_completed"`
	FilesTotal     int          `json:"files_total"`
}

type Store struct{ db *sql.DB }

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "shiying.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	schema := `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
	CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	CREATE TABLE IF NOT EXISTS personal_catalog(preview_id TEXT PRIMARY KEY,scope TEXT NOT NULL,content_id TEXT NOT NULL,payload TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(scope,content_id));
 CREATE TABLE IF NOT EXISTS authors(uid TEXT PRIMARY KEY,payload TEXT NOT NULL,updated_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS author_catalog(preview_id TEXT PRIMARY KEY,content_id TEXT NOT NULL UNIQUE,author_uid TEXT NOT NULL,payload TEXT NOT NULL,updated_at TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS author_catalog_by_author ON author_catalog(author_uid);
 CREATE TABLE IF NOT EXISTS tasks (
	 id TEXT PRIMARY KEY, video_id TEXT NOT NULL UNIQUE, title TEXT NOT NULL, author TEXT NOT NULL,
	 cover TEXT NOT NULL, duration INTEGER NOT NULL, width INTEGER NOT NULL, height INTEGER NOT NULL,
	 source_url TEXT NOT NULL, media_urls TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'queued',
	 downloaded INTEGER NOT NULL DEFAULT 0, total INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
	 favorite INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 0
	);
	`
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Additive migrations preserve the user's SQLite rows, cookies and old MP4s.
	for _, column := range []struct{ name, definition string }{{"content_json", "TEXT NOT NULL DEFAULT '{}'"}, {"files_json", "TEXT NOT NULL DEFAULT '[]'"}, {"files_completed", "INTEGER NOT NULL DEFAULT 0"}, {"files_total", "INTEGER NOT NULL DEFAULT 0"}} {
		rows, e := db.Query("PRAGMA table_info(tasks)")
		if e != nil {
			db.Close()
			return nil, e
		}
		found := false
		for rows.Next() {
			var cid, notNull, pk int
			var name, typ string
			var defaultValue any
			if e = rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); e != nil {
				rows.Close()
				db.Close()
				return nil, e
			}
			if name == column.name {
				found = true
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			db.Close()
			return nil, e
		}
		if !found {
			if _, e = db.Exec("ALTER TABLE tasks ADD COLUMN " + column.name + " " + column.definition); e != nil {
				db.Close()
				return nil, e
			}
		}
	}
	if _, err = db.Exec("UPDATE tasks SET status='queued',downloaded=0,total=0,media_urls='[]',content_json=json_set(content_json,'$.fetched_at','') WHERE status IN ('downloading','queued')"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec("INSERT OR IGNORE INTO settings(key,value)VALUES('cookie_revision',?)", randomID()); err != nil {
		db.Close()
		return nil, err
	}
	if err = os.Chmod(filepath.Join(dir, "shiying.db"), 0600); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
func now() string             { return time.Now().UTC().Format(time.RFC3339Nano) }
func (s *Store) Cookie() (string, error) {
	var v string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key='cookie'").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
func (s *Store) Credentials() (cookie, revision string, err error) {
	err = s.db.QueryRow(`SELECT COALESCE(MAX(CASE WHEN key='cookie' THEN value END),''),COALESCE(MAX(CASE WHEN key='cookie_revision' THEN value END),'') FROM settings`).Scan(&cookie, &revision)
	return
}
func (s *Store) SetCookie(value string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRow("SELECT value FROM settings WHERE key='cookie'").Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.Exec("INSERT INTO settings(key,value)VALUES('cookie',?) ON CONFLICT(key)DO UPDATE SET value=excluded.value", value); err != nil {
		return err
	}
	if value != previous {
		if _, err = tx.Exec("INSERT INTO settings(key,value)VALUES('cookie_revision',?) ON CONFLICT(key)DO UPDATE SET value=excluded.value", randomID()); err != nil {
			return err
		}
		if _, err = tx.Exec("DELETE FROM personal_catalog"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const columns = `id,video_id,title,author,cover,duration,width,height,source_url,media_urls,status,downloaded,total,error,favorite,created_at,updated_at,attempt,content_json,files_json,files_completed,files_total`

type savedContent struct {
	ContentInfo
	Manifest Manifest `json:"media"`
}

type scanner interface{ Scan(...any) error }

func scanTask(row scanner) (Task, error) {
	var t Task
	var urls, content, files string
	err := row.Scan(&t.ID, &t.Video.ID, &t.Title, &t.Author, &t.Cover, &t.Duration, &t.Width, &t.Height, &t.SourceURL, &urls, &t.Status, &t.Downloaded, &t.Total, &t.Error, &t.Favorite, &t.CreatedAt, &t.UpdatedAt, &t.Attempt, &content, &files, &t.FilesCompleted, &t.FilesTotal)
	if err == nil {
		err = json.Unmarshal([]byte(urls), &t.MediaURLs)
	}
	if err == nil {
		var state savedContent
		err = json.Unmarshal([]byte(content), &state)
		t.ContentInfo = state.ContentInfo
		t.Manifest = state.Manifest
		if t.Kind == "" {
			t.Kind = "video"
		}
		if t.Description == "" {
			t.Description = t.Title
		}
	}
	if err == nil {
		err = json.Unmarshal([]byte(files), &t.Files)
	}
	return t, err
}
func (s *Store) Task(id string) (Task, error) {
	return scanTask(s.db.QueryRow("SELECT "+columns+" FROM tasks WHERE id=?", id))
}
func (s *Store) ByVideo(id string) (Task, error) {
	return scanTask(s.db.QueryRow("SELECT "+columns+" FROM tasks WHERE video_id=?", id))
}
func (s *Store) List() ([]Task, error) {
	rows, err := s.db.Query("SELECT " + columns + " FROM tasks ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]Task, 0)
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			return nil, e
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
func (s *Store) Add(v Video) (Task, error) {
	id := randomID()
	ts := now()
	if v.Kind == "" {
		v.Kind = "video"
	}
	urls, _ := json.Marshal(v.MediaURLs)
	content, _ := json.Marshal(savedContent{v.ContentInfo, v.Manifest})
	_, err := s.db.Exec(`INSERT INTO tasks(id,video_id,title,author,cover,duration,width,height,source_url,media_urls,created_at,updated_at,content_json,files_total) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, v.ID, v.Title, v.Author, v.Cover, v.Duration, v.Width, v.Height, v.SourceURL, string(urls), ts, ts, string(content), len(buildPlan(v, maxVideoBytes, true).Items))
	if err != nil {
		return Task{}, err
	}
	return s.Task(id)
}
func (s *Store) Claim() (Task, error) {
	return scanTask(s.db.QueryRow(`UPDATE tasks SET status='downloading',attempt=attempt+1,updated_at=? WHERE id=(SELECT id FROM tasks WHERE status='queued' ORDER BY created_at LIMIT 1) AND status='queued' RETURNING `+columns, now()))
}
func (s *Store) Progress(t Task, n, total int64) error {
	_, err := s.db.Exec(`UPDATE tasks SET downloaded=?,total=?,updated_at=? WHERE id=? AND attempt=? AND status='downloading'`, n, total, now(), t.ID, t.Attempt)
	return err
}
func (s *Store) Finish(t Task, status, message string) error {
	_, err := s.db.Exec(`UPDATE tasks SET status=?,error=?,updated_at=? WHERE id=? AND attempt=? AND status='downloading'`, status, message, now(), t.ID, t.Attempt)
	return err
}

// Updates are fenced by attempt so canceled, deleted, or retried jobs cannot be revived.
func (s *Store) SaveContent(t Task) error {
	content, _ := json.Marshal(savedContent{t.ContentInfo, t.Manifest})
	urls, _ := json.Marshal(t.MediaURLs)
	_, err := s.db.Exec(`UPDATE tasks SET title=?,author=?,cover=?,duration=?,width=?,height=?,source_url=?,media_urls=?,content_json=?,updated_at=? WHERE id=? AND attempt=? AND status='downloading'`, t.Title, t.Author, t.Cover, t.Duration, t.Width, t.Height, t.SourceURL, string(urls), string(content), now(), t.ID, t.Attempt)
	return err
}
func (s *Store) SaveFiles(t Task, files []StoredFile, downloaded, total int64) error {
	payload, _ := json.Marshal(files)
	done := 0
	for _, f := range files {
		if f.State == "done" {
			done++
		}
	}
	_, err := s.db.Exec(`UPDATE tasks SET files_json=?,files_completed=?,files_total=?,downloaded=?,total=?,updated_at=? WHERE id=? AND attempt=? AND status='downloading'`, string(payload), done, len(files), downloaded, total, now(), t.ID, t.Attempt)
	return err
}

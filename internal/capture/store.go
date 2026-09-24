// Package capture keeps requests Pier proxied, so pier replay can send them
// again. Each project has one SQLite file at .pier/capture.db.
package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go: no cgo, nothing to install
)

// MaxBody is the most of each body a capture keeps.
const MaxBody = 1 << 20

// redacted headers carry credentials; a capture file should not.
var redacted = []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie"}

// Exchange is one request and its response.
type Exchange struct {
	ID       int64
	Time     time.Time
	Service  string
	Host     string // as the client sent it: the .local or Tailscale name
	Method   string
	URL      string // path and query
	Client   string
	Duration time.Duration

	RequestHeader    http.Header
	RequestBody      []byte
	RequestTruncated bool

	Status            int
	ResponseHeader    http.Header
	ResponseBody      []byte
	ResponseTruncated bool
}

// Store is a project's capture file.
type Store struct {
	db *sql.DB
}

// PathFor is the capture file of the project at root.
func PathFor(root string) string { return filepath.Join(root, ".pier", "capture.db") }

const schema = `
CREATE TABLE IF NOT EXISTS exchanges (
	id                 INTEGER PRIMARY KEY AUTOINCREMENT,
	time               INTEGER NOT NULL,
	service            TEXT    NOT NULL,
	host               TEXT    NOT NULL,
	method             TEXT    NOT NULL,
	url                TEXT    NOT NULL,
	client             TEXT    NOT NULL,
	duration_ms        INTEGER NOT NULL,
	request_headers    TEXT    NOT NULL,
	request_body       BLOB,
	request_truncated  INTEGER NOT NULL,
	status             INTEGER NOT NULL,
	response_headers   TEXT    NOT NULL,
	response_body      BLOB,
	response_truncated INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS exchanges_by_time ON exchanges (service, time);`

// Open opens or creates the capture file at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// WAL lets pier replay read while the daemon writes.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("prepare %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// OpenExisting opens path for reading, and reports ErrNoCaptures when the
// project never captured anything.
func OpenExisting(path string) (*Store, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoCaptures
	}
	return Open(path)
}

// ErrNoCaptures means the project has no capture file yet.
var ErrNoCaptures = errors.New("no captured requests yet")

// Close closes the file.
func (s *Store) Close() error { return s.db.Close() }

// Add saves exchanges in one transaction.
func (s *Store) Add(ctx context.Context, exchanges []Exchange) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	insert, err := tx.PrepareContext(ctx, `INSERT INTO exchanges (time, service, host, method, url, client, duration_ms,
		request_headers, request_body, request_truncated, status, response_headers, response_body, response_truncated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	for _, e := range exchanges {
		requestHeader, _ := json.Marshal(redact(e.RequestHeader))
		responseHeader, _ := json.Marshal(redact(e.ResponseHeader))
		if _, err := insert.ExecContext(ctx, e.Time.UnixNano(), e.Service, e.Host, e.Method, e.URL, e.Client, e.Duration.Milliseconds(),
			string(requestHeader), e.RequestBody, e.RequestTruncated, e.Status, string(responseHeader), e.ResponseBody, e.ResponseTruncated); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Query picks exchanges. The zero value picks the newest of every service.
type Query struct {
	Service string
	Since   time.Time
	Limit   int
	// Oldest orders oldest first, the order replays go out in.
	Oldest bool
}

// List returns the exchanges q picks.
func (s *Store) List(ctx context.Context, q Query) ([]Exchange, error) {
	where := []string{"time >= ?"}
	args := []any{q.Since.UnixNano()}
	if q.Since.IsZero() {
		args[0] = int64(0)
	}
	if q.Service != "" {
		where = append(where, "service = ?")
		args = append(args, q.Service)
	}
	order := "DESC"
	if q.Oldest {
		order = "ASC"
	}
	query := "SELECT " + columns + " FROM exchanges WHERE " + strings.Join(where, " AND ") + " ORDER BY time " + order + ", id " + order
	if q.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Exchange
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Get returns one exchange by id.
func (s *Store) Get(ctx context.Context, id int64) (Exchange, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM exchanges WHERE id = ?", id)
	e, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Exchange{}, fmt.Errorf("no captured request #%d; run pier replay to list them", id)
	}
	return e, err
}

// Prune deletes a service's exchanges older than before.
func (s *Store) Prune(ctx context.Context, service string, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM exchanges WHERE service = ? AND time < ?", service, before.UnixNano())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// PruneExcept deletes every exchange of services not in keep: ones whose
// capture was turned off or removed.
func (s *Store) PruneExcept(ctx context.Context, keep []string) error {
	if len(keep) == 0 {
		_, err := s.db.ExecContext(ctx, "DELETE FROM exchanges")
		return err
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(keep)), ",")
	args := make([]any, len(keep))
	for i, name := range keep {
		args[i] = name
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM exchanges WHERE service NOT IN ("+marks+")", args...)
	return err
}

const columns = `id, time, service, host, method, url, client, duration_ms, request_headers, request_body,
	request_truncated, status, response_headers, response_body, response_truncated`

type scanner interface{ Scan(dest ...any) error }

func scan(row scanner) (Exchange, error) {
	var e Exchange
	var at, durationMS int64
	var requestHeader, responseHeader string
	err := row.Scan(&e.ID, &at, &e.Service, &e.Host, &e.Method, &e.URL, &e.Client, &durationMS, &requestHeader,
		&e.RequestBody, &e.RequestTruncated, &e.Status, &responseHeader, &e.ResponseBody, &e.ResponseTruncated)
	if err != nil {
		return Exchange{}, err
	}
	e.Time = time.Unix(0, at)
	e.Duration = time.Duration(durationMS) * time.Millisecond
	_ = json.Unmarshal([]byte(requestHeader), &e.RequestHeader)
	_ = json.Unmarshal([]byte(responseHeader), &e.ResponseHeader)
	return e, nil
}

func redact(header http.Header) http.Header {
	out := header.Clone()
	for _, name := range redacted {
		if _, ok := out[name]; ok {
			out[name] = []string{"[redacted]"}
		}
	}
	return out
}

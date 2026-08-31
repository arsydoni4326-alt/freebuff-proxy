// Package tokendb provides a persistent SQLite-backed store for AUTH_TOKENS.
//
// Tokens are the authoritative source of truth: the pool reads from this
// database at startup and mutations (add/remove) are applied both in-memory
// and persisted atomically.  The legacy AUTH_TOKENS= line in .env is
// migrated on first open so existing deployments upgrade transparently.
package tokendb

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps a SQLite database that persists AUTH_TOKENS.
type DB struct {
	db   *sql.DB
	path string
	mu   sync.Mutex
	log  *slog.Logger
}

// Open opens or creates the SQLite database at path.
func Open(path string, log *slog.Logger) (*DB, error) {
	if log == nil {
		log = slog.Default()
	}
	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("tokendb: mkdir %s: %w", dir, err)
		}
	}
	dsn := path + "?_journal=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("tokendb: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("tokendb: ping %s: %w", path, err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS auth_tokens (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			token      TEXT    NOT NULL UNIQUE,
			created_at TEXT    NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("tokendb: create table: %w", err)
	}
	return &DB{db: db, path: path, log: log}, nil
}

// Close closes the underlying database.
func (d *DB) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// List returns all persisted tokens in insertion order.
func (d *DB) List() ([]string, error) {
	rows, err := d.db.Query(`SELECT token FROM auth_tokens ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("tokendb: list: %w", err)
	}
	defer rows.Close()
	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("tokendb: list scan: %w", err)
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// Count returns the number of persisted tokens.
func (d *DB) Count() (int, error) {
	var n int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM auth_tokens`).Scan(&n); err != nil {
		return 0, fmt.Errorf("tokendb: count: %w", err)
	}
	return n, nil
}

// Add inserts a token if it does not already exist. Returns the row id.
func (d *DB) Add(token string) (int64, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, fmt.Errorf("tokendb: token must not be empty")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.db.Exec(
		`INSERT OR IGNORE INTO auth_tokens (token, created_at) VALUES (?, ?)`,
		token, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("tokendb: add: %w", err)
	}
	id, _ := res.LastInsertId()
	d.log.Info("token added to database", "id", id, "token_prefix", prefix(token))
	return id, nil
}

// Remove deletes a token by its exact value. Returns rows affected.
func (d *DB) Remove(token string) (int64, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.db.Exec(`DELETE FROM auth_tokens WHERE token = ?`, token)
	if err != nil {
		return 0, fmt.Errorf("tokendb: remove: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		d.log.Info("token removed from database", "token_prefix", prefix(token))
	}
	return n, nil
}

// RemoveLast deletes the most-recently-inserted token.
func (d *DB) RemoveLast() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var token string
	err := d.db.QueryRow(`SELECT token FROM auth_tokens ORDER BY id DESC LIMIT 1`).Scan(&token)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("tokendb: remove last: %w", err)
	}
	if _, err := d.db.Exec(`DELETE FROM auth_tokens WHERE token = ?`, token); err != nil {
		return "", fmt.Errorf("tokendb: remove last delete: %w", err)
	}
	d.log.Info("token removed from database (last)", "token_prefix", prefix(token))
	return token, nil
}

// RemoveAll clears all tokens from the database.
func (d *DB) RemoveAll() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.db.Exec(`DELETE FROM auth_tokens`); err != nil {
		return fmt.Errorf("tokendb: remove all: %w", err)
	}
	d.log.Info("all tokens removed from database")
	return nil
}

// MigrateFromEnv seeds the database from a comma-separated AUTH_TOKENS
// string (typically read from .env). Tokens already present are skipped.
func (d *DB) MigrateFromEnv(envValue string) (int, error) {
	if strings.TrimSpace(envValue) == "" {
		return 0, nil
	}
	existing, err := d.List()
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{}, len(existing))
	for _, t := range existing {
		seen[t] = struct{}{}
	}
	tokens := splitList(envValue)
	var added int
	for _, t := range tokens {
		if _, ok := seen[t]; ok {
			continue
		}
		if _, err := d.Add(t); err != nil {
			return added, fmt.Errorf("tokendb: migrate token: %w", err)
		}
		added++
	}
	if added > 0 {
		d.log.Info("migrated tokens from AUTH_TOKENS env", "added", added)
	}
	return added, nil
}

// Tokens returns a sorted snapshot of all tokens.
func (d *DB) Tokens() []string {
	tokens, err := d.List()
	if err != nil {
		return nil
	}
	sorted := make([]string, len(tokens))
	copy(sorted, tokens)
	sort.Strings(sorted)
	return sorted
}

func splitList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func prefix(token string) string {
	if len(token) > 8 {
		return token[:8] + "…"
	}
	return token
}

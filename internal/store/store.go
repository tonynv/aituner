// Package store is the SQLite datastore. Every table carries tenant_id and the only way to reach
// tenant data is through a Tenant handle from DB.ForTenant, which injects tenant_id into every query.
// SQLite has no row level security, so this is the enforcement point (see SPECS/SPEC.md, D1).
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var (
	ErrNotFound   = errors.New("not found")
	ErrWrongPhase = errors.New("wrong phase")
)

type DB struct{ sql *sql.DB }

// Open creates the database (dir 0700, file 0600), enables WAL and foreign keys, and applies migrations.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		f.Close()
	} else {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	s, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s.SetMaxOpenConns(1) // single writer; the workload is tiny and this avoids SQLITE_BUSY entirely
	d := &DB{sql: s}
	if err := d.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error { return d.sql.Close() }

func (d *DB) migrate() error {
	if _, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, n := range names {
		var one int
		err := d.sql.QueryRow(`SELECT 1 FROM schema_migrations WHERE name=?`, n).Scan(&one)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		body, err := migrationFS.ReadFile(n)
		if err != nil {
			return err
		}
		tx, err := d.sql.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", n, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(name, applied_at) VALUES (?,?)`, n, now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func now() int64 { return time.Now().UnixMilli() }

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b)
}

// EnsureTenant returns the id of the named tenant, creating it if needed.
func (d *DB) EnsureTenant(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("tenant name required")
	}
	var id string
	err := d.sql.QueryRowContext(ctx, `SELECT id FROM tenants WHERE name=?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = newID()
	if _, err := d.sql.ExecContext(ctx, `INSERT INTO tenants(id,name,created_at) VALUES (?,?,?)`, id, name, now()); err != nil {
		return "", err
	}
	return id, nil
}

// ForTenant returns a handle scoped to one tenant. There is deliberately no unscoped accessor.
func (d *DB) ForTenant(id string) *Tenant { return &Tenant{db: d.sql, id: id} }

type Tenant struct {
	db *sql.DB
	id string
}

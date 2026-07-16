// Package migratex wraps golang-migrate with project conventions.
package migratex

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// Migrator executes SQL migrations from a directory.
type Migrator struct {
	DSN string
	Dir string
}

// New creates a Migrator. dir can be a local path or file:// URL.
func New(dsn, dir string) *Migrator {
	return &Migrator{DSN: dsn, Dir: dir}
}

// Up applies all pending migrations.
func Up(dsn, dir string) error {
	return New(dsn, dir).Up()
}

// Down rolls back a positive number of migration steps.
func Down(dsn, dir string, steps int) error {
	return New(dsn, dir).Down(steps)
}

// Version returns current migration version and dirty flag.
func Version(dsn, dir string) (uint, bool, error) {
	return New(dsn, dir).Version()
}

// Force sets the migration version and clears dirty state.
func Force(dsn, dir string, version int) error {
	return New(dsn, dir).Force(version)
}

// Up applies all pending migrations.
func (m *Migrator) Up() error {
	mm, err := m.open()
	if err != nil {
		return err
	}
	defer mm.Close()
	err = mm.Up()
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return err
}

// Down rolls back a positive number of migration steps.
func (m *Migrator) Down(steps int) error {
	if steps <= 0 {
		return fmt.Errorf("migratex: steps must be positive")
	}
	mm, err := m.open()
	if err != nil {
		return err
	}
	defer mm.Close()
	err = mm.Steps(-steps)
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return err
}

// Version returns current migration version and dirty flag.
func (m *Migrator) Version() (uint, bool, error) {
	mm, err := m.open()
	if err != nil {
		return 0, false, err
	}
	defer mm.Close()
	version, dirty, err := mm.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return version, dirty, err
}

// Force sets the migration version and clears dirty state.
func (m *Migrator) Force(version int) error {
	mm, err := m.open()
	if err != nil {
		return err
	}
	defer mm.Close()
	return mm.Force(version)
}

func (m *Migrator) open() (*migrate.Migrate, error) {
	if m.DSN == "" {
		return nil, fmt.Errorf("migratex: empty dsn")
	}
	sourceURL, err := SourceURL(m.Dir)
	if err != nil {
		return nil, err
	}
	mm, err := migrate.New(sourceURL, m.DSN)
	if err != nil {
		return nil, fmt.Errorf("migratex: open: %w", err)
	}
	return mm, nil
}

// SourceURL converts a local migration directory to a file:// URL.
func SourceURL(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("migratex: empty migration dir")
	}
	if strings.HasPrefix(dir, "file://") {
		return dir, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("migratex: abs dir: %w", err)
	}
	path := filepath.ToSlash(abs)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String(), nil
}

var migrationNameRe = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

// Create creates paired up/down SQL migration files.
func Create(dir, name string) (upPath, downPath string, err error) {
	if dir == "" {
		return "", "", fmt.Errorf("migratex: empty migration dir")
	}
	slug := slugName(name)
	if slug == "" {
		return "", "", fmt.Errorf("migratex: empty migration name")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("migratex: mkdir: %w", err)
	}
	version := nextMigrationVersion()
	upPath = filepath.Join(dir, version+"_"+slug+".up.sql")
	downPath = filepath.Join(dir, version+"_"+slug+".down.sql")
	if err := writeNewFile(upPath); err != nil {
		return "", "", err
	}
	if err := writeNewFile(downPath); err != nil {
		_ = os.Remove(upPath)
		return "", "", err
	}
	return upPath, downPath, nil
}

var lastMigrationVersion atomic.Int64

func nextMigrationVersion() string {
	candidate := time.Now().UTC().UnixNano()
	for {
		previous := lastMigrationVersion.Load()
		if candidate <= previous {
			candidate = previous + 1
		}
		if lastMigrationVersion.CompareAndSwap(previous, candidate) {
			return strconv.FormatInt(candidate, 10)
		}
	}
}

func slugName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = migrationNameRe.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	return name
}

func writeNewFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("migratex: create %s: %w", filepath.Base(path), err)
	}
	defer f.Close()
	_, err = f.WriteString("-- Write your SQL here.\n")
	return err
}

// EscapeDSN URL-encodes a PostgreSQL DSN. It is not shell escaping.
func EscapeDSN(dsn string) string {
	return url.QueryEscape(dsn)
}

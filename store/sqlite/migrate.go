package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, err
	}

	migrations := make([]migration, 0, len(entries))
	for _, e := range entries {
		num, name, ok := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q: want NNNN_name.sql", e.Name())
		}

		version, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("migration %q: bad version: %w", e.Name(), err)
		}

		sql, err := migrationFiles.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, err
		}

		migrations = append(migrations, migration{version: version, name: name, sql: string(sql)})
	}

	slices.SortFunc(migrations, func(a, b migration) int { return a.version - b.version })

	return migrations, nil
}

// Transactions lock the database at BEGIN (see dsn), so a second process
// opening the same file waits and then skips what's already applied.
func migrate(ctx context.Context, db *sql.DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL DEFAULT (unixepoch('subsec') * 1000)
	) STRICT`); err != nil {
		return fmt.Errorf("sqlite: create schema_migrations: %w", err)
	}

	for _, m := range migrations {
		if err := apply(ctx, db, m); err != nil {
			return fmt.Errorf("sqlite: migration %04d_%s: %w", m.version, m.name, err)
		}
	}

	return nil
}

func apply(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var recorded string
	err = tx.QueryRowContext(ctx, `SELECT name FROM schema_migrations WHERE version = ?`, m.version).Scan(&recorded)
	switch {
	case err == nil && recorded == m.name:
		return nil
	case err == nil:
		return fmt.Errorf("version %d is already recorded as %q; number new migrations after the highest recorded version", m.version, recorded)
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
		return err
	}

	return tx.Commit()
}

// Package sqlite implements store.Store with SQLite.
//
// All writes share one connection, which keeps SQLite's single writer lock
// from turning into SQLITE_BUSY errors. Reads go through a separate
// read-only pool and, with WAL on, don't wait for writes.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"runtime"

	"github.com/VTGare/gatoraid/store"

	_ "modernc.org/sqlite"
)

var _ store.Store = (*Store)(nil)

type Store struct {
	write *sql.DB
	read  *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	write, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	write.SetMaxOpenConns(1)

	// Open the writer first so the file exists and WAL is on before any
	// reader connects.
	if err := write.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("sqlite: open %s: %w", path, err), write.Close())
	}

	read, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, errors.Join(err, write.Close())
	}
	read.SetMaxOpenConns(max(4, runtime.NumCPU()))

	s := &Store{write: write, read: read}
	if err := migrate(ctx, write); err != nil {
		return nil, errors.Join(err, s.Close())
	}

	return s, nil
}

func (s *Store) Close() error {
	return errors.Join(s.read.Close(), s.write.Close())
}

func dsn(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	if readOnly {
		q.Add("_pragma", "query_only(ON)")
	} else {
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "synchronous(NORMAL)")
		// Lock at BEGIN instead of on the first write, so transactions
		// wait for each other instead of failing.
		q.Set("_txlock", "immediate")
	}

	return "file:" + path + "?" + q.Encode()
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}

	return tx.Commit()
}

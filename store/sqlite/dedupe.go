package sqlite

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) MarkSeen(ctx context.Context, kind string, keys []string) ([]string, error) {
	var fresh []string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		for _, key := range keys {
			var seen bool
			if err := tx.QueryRowContext(ctx,
				`SELECT EXISTS (SELECT 1 FROM dedupe WHERE kind = ? AND key = ?)`, kind, key).Scan(&seen); err != nil {
				return err
			}
			if !seen {
				fresh = append(fresh, key)
			}

			if _, err := tx.ExecContext(ctx, `
				INSERT INTO dedupe (kind, key, seen_at) VALUES (?, ?, ?)
				ON CONFLICT DO UPDATE SET seen_at = excluded.seen_at`, kind, key, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return fresh, nil
}

func (s *Store) PruneSeen(ctx context.Context, before time.Time) (int, error) {
	r, err := s.write.ExecContext(ctx, `DELETE FROM dedupe WHERE seen_at < ?`, before.UnixMilli())
	if err != nil {
		return 0, err
	}

	n, err := r.RowsAffected()
	return int(n), err
}

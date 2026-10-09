package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/VTGare/gatoraid/store"
)

func (s *Store) Guild(ctx context.Context, guildID string) (*store.Guild, error) {
	return scanGuild(s.read.QueryRowContext(ctx,
		`SELECT id, settings, joined_at, left_at FROM guilds WHERE id = ?`, guildID))
}

func (s *Store) Guilds(ctx context.Context) ([]store.Guild, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT id, settings, joined_at, left_at FROM guilds ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []store.Guild
	for rows.Next() {
		g, err := scanGuild(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}

	return out, rows.Err()
}

func (s *Store) JoinGuild(ctx context.Context, guildID string) (*store.Guild, store.JoinKind, error) {
	var (
		g    *store.Guild
		kind = store.JoinExisting
	)

	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var leftAt sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT left_at FROM guilds WHERE id = ?`, guildID).Scan(&leftAt)

		now := time.Now().UnixMilli()
		switch {
		case errors.Is(err, sql.ErrNoRows):
			kind = store.JoinNew
			settings, err := store.EncodeSettings(store.DefaultSettings())
			if err != nil {
				return err
			}

			if _, err := tx.ExecContext(ctx,
				`INSERT INTO guilds (id, settings, joined_at) VALUES (?, ?, ?)`, guildID, string(settings), now); err != nil {
				return err
			}
		case err != nil:
			return err
		case leftAt.Valid:
			kind = store.JoinRestored
			if _, err := tx.ExecContext(ctx,
				`UPDATE guilds SET left_at = NULL, joined_at = ? WHERE id = ?`, now, guildID); err != nil {
				return err
			}
		}

		g, err = scanGuild(tx.QueryRowContext(ctx,
			`SELECT id, settings, joined_at, left_at FROM guilds WHERE id = ?`, guildID))
		return err
	})
	if err != nil {
		return nil, store.JoinExisting, err
	}

	return g, kind, nil
}

func (s *Store) LeaveGuild(ctx context.Context, guildID string, at time.Time) error {
	_, err := s.write.ExecContext(ctx,
		`UPDATE guilds SET left_at = ? WHERE id = ? AND left_at IS NULL`, at.UnixMilli(), guildID)
	return err
}

func (s *Store) ReconcileGuilds(ctx context.Context, present []string, at time.Time) ([]string, error) {
	keep := make(map[string]bool, len(present))
	for _, id := range present {
		keep[id] = true
	}

	var left []string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM guilds WHERE left_at IS NULL`)
		if err != nil {
			return err
		}

		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return errors.Join(err, rows.Close())
			}
			if !keep[id] {
				left = append(left, id)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}

		for _, id := range left {
			if _, err := tx.ExecContext(ctx, `UPDATE guilds SET left_at = ? WHERE id = ?`, at.UnixMilli(), id); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return left, nil
}

func (s *Store) UpdateGuildSettings(ctx context.Context, guildID string, settings store.Settings) error {
	data, err := store.EncodeSettings(settings)
	if err != nil {
		return err
	}

	res, err := s.write.ExecContext(ctx, `UPDATE guilds SET settings = ? WHERE id = ?`, string(data), guildID)
	if err != nil {
		return err
	}

	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrGuildNotFound
	}

	return nil
}

func (s *Store) PurgeGuilds(ctx context.Context, leftBefore time.Time) (int, error) {
	res, err := s.write.ExecContext(ctx,
		`DELETE FROM guilds WHERE left_at IS NOT NULL AND left_at < ?`, leftBefore.UnixMilli())
	if err != nil {
		return 0, err
	}

	n, err := res.RowsAffected()
	return int(n), err
}

func scanGuild(row interface{ Scan(dest ...any) error }) (*store.Guild, error) {
	var (
		g        store.Guild
		settings []byte
		joinedAt int64
		leftAt   sql.NullInt64
	)

	err := row.Scan(&g.ID, &settings, &joinedAt, &leftAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrGuildNotFound
	}
	if err != nil {
		return nil, err
	}

	if g.Settings, err = store.DecodeSettings(settings); err != nil {
		return nil, err
	}

	g.JoinedAt = time.UnixMilli(joinedAt)
	if leftAt.Valid {
		t := time.UnixMilli(leftAt.Int64)
		g.LeftAt = &t
	}

	return &g, nil
}

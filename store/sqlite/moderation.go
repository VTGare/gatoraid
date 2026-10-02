package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/VTGare/gatoraid/store"
)

func (s *Store) AllBlacklists(ctx context.Context) ([]store.BlacklistEntry, error) {
	rows, err := s.read.QueryContext(ctx, `
		SELECT guild_id, yt_channel_id, name, reason, added_by, created_at
		FROM blacklist ORDER BY created_at, yt_channel_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []store.BlacklistEntry
	for rows.Next() {
		e, err := scanBlacklistEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}

	return out, rows.Err()
}

func (s *Store) AllFilters(ctx context.Context) ([]store.Filter, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT guild_id, kind, pattern, created_at FROM filters ORDER BY created_at, pattern`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []store.Filter
	for rows.Next() {
		var (
			f         store.Filter
			kind      string
			createdAt int64
		)
		if err := rows.Scan(&f.GuildID, &kind, &f.Pattern, &createdAt); err != nil {
			return nil, err
		}
		f.Kind = store.FilterKind(kind)
		f.CreatedAt = time.UnixMilli(createdAt)
		out = append(out, f)
	}

	return out, rows.Err()
}

func (s *Store) AddToBlacklist(ctx context.Context, e store.BlacklistEntry) (bool, error) {
	return s.insertForGuild(ctx, e.GuildID, `
		INSERT INTO blacklist (guild_id, yt_channel_id, name, reason, added_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		e.GuildID, e.ChannelID, e.Name, e.Reason, e.AddedBy, time.Now().UnixMilli())
}

func (s *Store) RemoveFromBlacklist(ctx context.Context, guildID, channelID string) (*store.BlacklistEntry, error) {
	var removed *store.BlacklistEntry
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		query := `SELECT guild_id, yt_channel_id, name, reason, added_by, created_at FROM blacklist
			WHERE guild_id = ? AND yt_channel_id = ?`
		args := []any{guildID, channelID}
		if channelID == "" {
			query = `SELECT guild_id, yt_channel_id, name, reason, added_by, created_at FROM blacklist
				WHERE guild_id = ? ORDER BY created_at DESC, yt_channel_id DESC LIMIT 1`
			args = args[:1]
		}

		e, err := scanBlacklistEntry(tx.QueryRowContext(ctx, query, args...))
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotBlacklisted
		}
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM blacklist WHERE guild_id = ? AND yt_channel_id = ?`, guildID, e.ChannelID); err != nil {
			return err
		}
		removed = e
		return nil
	})

	return removed, err
}

func (s *Store) AddFilter(ctx context.Context, f store.Filter) (bool, error) {
	return s.insertForGuild(ctx, f.GuildID, `
		INSERT INTO filters (guild_id, kind, pattern, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		f.GuildID, string(f.Kind), f.Pattern, time.Now().UnixMilli())
}

func (s *Store) RemoveFilter(ctx context.Context, guildID string, kind store.FilterKind, pattern string) error {
	r, err := s.write.ExecContext(ctx,
		`DELETE FROM filters WHERE guild_id = ? AND kind = ? AND pattern = ?`, guildID, string(kind), pattern)
	if err != nil {
		return err
	}

	if n, err := r.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return store.ErrFilterNotFound
	}

	return nil
}

// insertForGuild runs an insert that ignores duplicates and reports whether
// it added a row. A missing guild is ErrGuildNotFound rather than a
// foreign key error.
func (s *Store) insertForGuild(ctx context.Context, guildID, query string, args ...any) (bool, error) {
	var created bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM guilds WHERE id = ?)`, guildID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return store.ErrGuildNotFound
		}

		r, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		created = n > 0
		return err
	})

	return created, err
}

func scanBlacklistEntry(row scanner) (*store.BlacklistEntry, error) {
	var (
		e         store.BlacklistEntry
		createdAt int64
	)
	if err := row.Scan(&e.GuildID, &e.ChannelID, &e.Name, &e.Reason, &e.AddedBy, &createdAt); err != nil {
		return nil, err
	}
	e.CreatedAt = time.UnixMilli(createdAt)
	return &e, nil
}

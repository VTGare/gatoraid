package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/VTGare/gatoraid/store"
)

const streamerColumns = `channel_id, name, channel_name, group_id, twitter, aliases, avatar_url,
	free_chat_streams, source, added_by_guild, updated_at, removed_at`

func (s *Store) StreamerGroups(ctx context.Context) ([]store.Group, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT id, name, parent_id, skip_auto_translate FROM streamer_groups ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var groups []store.Group
	for rows.Next() {
		var (
			g      store.Group
			parent sql.NullString
		)
		if err := rows.Scan(&g.ID, &g.Name, &parent, &g.SkipAutoTranslate); err != nil {
			return nil, err
		}
		g.ParentID = parent.String
		groups = append(groups, g)
	}

	return groups, rows.Err()
}

func (s *Store) Streamers(ctx context.Context) ([]store.Streamer, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+streamerColumns+` FROM streamers`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []store.Streamer
	for rows.Next() {
		st, err := scanStreamer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}

	return out, rows.Err()
}

func (s *Store) Streamer(ctx context.Context, channelID string) (*store.Streamer, error) {
	st, err := scanStreamer(s.read.QueryRowContext(ctx,
		`SELECT `+streamerColumns+` FROM streamers WHERE channel_id = ?`, channelID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrStreamerNotFound
	}

	return st, err
}

func (s *Store) SyncSeed(ctx context.Context, groups []store.Group, streamers []store.Streamer) (store.SeedResult, error) {
	var res store.SeedResult

	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// Groups reference each other, so check foreign keys at commit
		// instead of after every insert.
		if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
			return err
		}

		groupIDs := make([]any, 0, len(groups))
		for i, g := range groups {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO streamer_groups (id, name, parent_id, skip_auto_translate, position)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET
					name = excluded.name,
					parent_id = excluded.parent_id,
					skip_auto_translate = excluded.skip_auto_translate,
					position = excluded.position`,
				g.ID, g.Name, nullString(g.ParentID), g.SkipAutoTranslate, i); err != nil {
				return err
			}
			groupIDs = append(groupIDs, g.ID)
		}

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM streamer_groups WHERE id NOT IN (SELECT value FROM json_each(?))`,
			jsonArray(groupIDs)); err != nil {
			return err
		}

		sources, err := streamerSources(ctx, tx)
		if err != nil {
			return err
		}

		now := time.Now().UnixMilli()
		seedIDs := make([]any, 0, len(streamers))
		for _, st := range streamers {
			seedIDs = append(seedIDs, st.ChannelID)
			aliases, err := json.Marshal(nonNil(st.Aliases))
			if err != nil {
				return err
			}

			source, exists := sources[st.ChannelID]
			switch {
			case !exists:
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO streamers (channel_id, name, channel_name, group_id, twitter, aliases,
						free_chat_streams, source, updated_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, 'seed', ?)`,
					st.ChannelID, st.Name, st.ChannelName, nullString(st.GroupID), st.Twitter,
					string(aliases), st.FreeChatStreams, now); err != nil {
					return err
				}
				res.Added++
			case source == store.SourceOwner:
				// Once the owner's entry has been copied into the seed
				// (see /owner streamers export), the seed takes it back.
				r, err := tx.ExecContext(ctx, `
					UPDATE streamers SET source = 'seed', added_by_guild = NULL, removed_at = NULL, updated_at = ?
					WHERE channel_id = ? AND name IS ? AND channel_name IS ? AND group_id IS ? AND twitter IS ?
						AND aliases IS ? AND free_chat_streams IS ?`,
					now, st.ChannelID, st.Name, st.ChannelName, nullString(st.GroupID), st.Twitter,
					string(aliases), st.FreeChatStreams)
				if err != nil {
					return err
				}
				if n, _ := r.RowsAffected(); n > 0 {
					res.Returned++
				} else {
					res.Kept++
				}
			default:
				// Avatars come from Holodex, so the seed doesn't touch them.
				r, err := tx.ExecContext(ctx, `
					UPDATE streamers SET name = ?, channel_name = ?, group_id = ?, twitter = ?, aliases = ?,
						free_chat_streams = ?, source = 'seed', added_by_guild = NULL, removed_at = NULL, updated_at = ?
					WHERE channel_id = ? AND (name IS NOT ? OR channel_name IS NOT ? OR group_id IS NOT ?
						OR twitter IS NOT ? OR aliases IS NOT ? OR free_chat_streams IS NOT ? OR source IS NOT 'seed'
						OR removed_at IS NOT NULL)`,
					st.Name, st.ChannelName, nullString(st.GroupID), st.Twitter, string(aliases), st.FreeChatStreams, now,
					st.ChannelID, st.Name, st.ChannelName, nullString(st.GroupID), st.Twitter, string(aliases), st.FreeChatStreams)
				if err != nil {
					return err
				}
				if n, _ := r.RowsAffected(); n > 0 {
					res.Updated++
				}
			}
		}

		r, err := tx.ExecContext(ctx, `
			UPDATE streamers SET removed_at = ?
			WHERE source = 'seed' AND removed_at IS NULL AND channel_id NOT IN (SELECT value FROM json_each(?))`,
			now, jsonArray(seedIDs))
		if err != nil {
			return err
		}
		removed, err := r.RowsAffected()
		res.Removed = int(removed)
		return err
	})

	return res, err
}

func (s *Store) SaveStreamer(ctx context.Context, st store.Streamer) error {
	aliases, err := json.Marshal(nonNil(st.Aliases))
	if err != nil {
		return err
	}

	_, err = s.write.ExecContext(ctx, `
		INSERT INTO streamers (`+streamerColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT (channel_id) DO UPDATE SET
			removed_at = NULL,
			name = excluded.name,
			channel_name = excluded.channel_name,
			group_id = excluded.group_id,
			twitter = excluded.twitter,
			aliases = excluded.aliases,
			avatar_url = excluded.avatar_url,
			free_chat_streams = excluded.free_chat_streams,
			source = excluded.source,
			added_by_guild = excluded.added_by_guild,
			updated_at = excluded.updated_at`,
		st.ChannelID, st.Name, st.ChannelName, nullString(st.GroupID), st.Twitter, string(aliases), st.AvatarURL,
		st.FreeChatStreams, string(st.Source), nullString(st.AddedByGuild), time.Now().UnixMilli())

	return err
}

func (s *Store) UpdateAvatars(ctx context.Context, avatars map[string]string) (int, error) {
	changed := 0
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		for id, url := range avatars {
			r, err := tx.ExecContext(ctx,
				`UPDATE streamers SET avatar_url = ? WHERE channel_id = ? AND avatar_url IS NOT ?`, url, id, url)
			if err != nil {
				return err
			}
			n, err := r.RowsAffected()
			if err != nil {
				return err
			}
			changed += int(n)
		}
		return nil
	})

	return changed, err
}

func (s *Store) RemoveStreamer(ctx context.Context, channelID string) error {
	r, err := s.write.ExecContext(ctx,
		`UPDATE streamers SET removed_at = ? WHERE channel_id = ? AND removed_at IS NULL`,
		time.Now().UnixMilli(), channelID)
	if err != nil {
		return err
	}

	if n, err := r.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return store.ErrStreamerNotFound
	}

	return nil
}

func (s *Store) PurgeStreamers(ctx context.Context, removedBefore time.Time) (int, error) {
	r, err := s.write.ExecContext(ctx, `
		DELETE FROM streamers
		WHERE removed_at < ? AND NOT EXISTS (
			SELECT 1 FROM subscriptions WHERE target_kind = 'channel' AND target = streamers.channel_id)`,
		removedBefore.UnixMilli())
	if err != nil {
		return 0, err
	}

	n, err := r.RowsAffected()
	return int(n), err
}

func (s *Store) HideUnusedUserStreamers(ctx context.Context) (int, error) {
	r, err := s.write.ExecContext(ctx, `
		UPDATE streamers SET removed_at = ?
		WHERE source = 'user' AND removed_at IS NULL AND NOT EXISTS (
			SELECT 1 FROM subscriptions WHERE target_kind = 'channel' AND target = streamers.channel_id)`,
		time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}

	n, err := r.RowsAffected()
	return int(n), err
}

func streamerSources(ctx context.Context, tx *sql.Tx) (map[string]store.StreamerSource, error) {
	rows, err := tx.QueryContext(ctx, `SELECT channel_id, source FROM streamers`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	sources := make(map[string]store.StreamerSource)
	for rows.Next() {
		var id, source string
		if err := rows.Scan(&id, &source); err != nil {
			return nil, err
		}
		sources[id] = store.StreamerSource(source)
	}

	return sources, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanStreamer(row scanner) (*store.Streamer, error) {
	var (
		st             store.Streamer
		group, addedBy sql.NullString
		aliases        string
		source         string
		updatedAt      int64
		removedAt      sql.NullInt64
	)

	err := row.Scan(&st.ChannelID, &st.Name, &st.ChannelName, &group, &st.Twitter, &aliases, &st.AvatarURL,
		&st.FreeChatStreams, &source, &addedBy, &updatedAt, &removedAt)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(aliases), &st.Aliases); err != nil {
		return nil, err
	}

	st.GroupID = group.String
	st.AddedByGuild = addedBy.String
	st.Source = store.StreamerSource(source)
	st.UpdatedAt = time.UnixMilli(updatedAt)
	if removedAt.Valid {
		t := time.UnixMilli(removedAt.Int64)
		st.RemovedAt = &t
	}

	return &st, nil
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func jsonArray(v []any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

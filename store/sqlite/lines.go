package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/VTGare/gatoraid/store"
)

func (s *Store) SaveLines(ctx context.Context, lines []store.Line) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO relayed_lines (video_id, guild_id, discord_channel_id, discord_message_id,
				author_channel_id, author_name, body, kind, said_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()

		for _, l := range lines {
			if _, err := stmt.ExecContext(ctx, l.VideoID, nullString(l.GuildID), nullString(l.ChannelID),
				nullString(l.MessageID), l.AuthorChannelID, l.AuthorName, l.Body, string(l.Kind),
				l.SaidAt.UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
}

// An empty guildID reads the archive.
func (s *Store) VideoLines(ctx context.Context, videoID, guildID string) ([]store.Line, error) {
	rows, err := s.read.QueryContext(ctx, `
		SELECT video_id, guild_id, discord_channel_id, discord_message_id, author_channel_id, author_name,
			body, kind, said_at
		FROM relayed_lines
		WHERE video_id = ? AND guild_id IS ?
		ORDER BY said_at, id`, videoID, nullString(guildID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []store.Line
	for rows.Next() {
		var (
			l                       store.Line
			guild, channel, message sql.NullString
			kind                    string
			saidAt                  int64
		)
		if err := rows.Scan(&l.VideoID, &guild, &channel, &message, &l.AuthorChannelID, &l.AuthorName,
			&l.Body, &kind, &saidAt); err != nil {
			return nil, err
		}

		l.GuildID, l.ChannelID, l.MessageID = guild.String, channel.String, message.String
		l.Kind = store.LineKind(kind)
		l.SaidAt = time.UnixMilli(saidAt)
		out = append(out, l)
	}

	return out, rows.Err()
}

func (s *Store) PruneLines(ctx context.Context, guildBefore, archiveBefore time.Time) (int, error) {
	r, err := s.write.ExecContext(ctx, `
		DELETE FROM relayed_lines
		WHERE (guild_id IS NOT NULL AND said_at < ?) OR (guild_id IS NULL AND said_at < ?)`,
		guildBefore.UnixMilli(), archiveBefore.UnixMilli())
	if err != nil {
		return 0, err
	}

	n, err := r.RowsAffected()
	return int(n), err
}

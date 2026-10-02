package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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

const lineColumns = `video_id, guild_id, discord_channel_id, discord_message_id, author_channel_id,
	author_name, body, kind, said_at`

// An empty guildID reads the archive.
func (s *Store) VideoLines(ctx context.Context, videoID, guildID string) ([]store.Line, error) {
	return s.queryLines(ctx, `SELECT `+lineColumns+` FROM relayed_lines
		WHERE video_id = ? AND guild_id IS ?
		ORDER BY said_at, id`, videoID, nullString(guildID))
}

func (s *Store) LineByMessage(ctx context.Context, guildID, messageID string) (*store.Line, error) {
	l, err := scanLine(s.read.QueryRowContext(ctx, `SELECT `+lineColumns+` FROM relayed_lines
		WHERE guild_id = ? AND discord_message_id = ?`, guildID, messageID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrLineNotFound
	}
	return l, err
}

func (s *Store) RecentAuthors(ctx context.Context, guildID, query string, limit int) ([]store.Line, error) {
	return s.queryLines(ctx, `SELECT `+lineColumns+` FROM relayed_lines
		WHERE id IN (
			SELECT MAX(id) FROM relayed_lines
			WHERE guild_id = ? AND author_name LIKE ? ESCAPE '\'
			GROUP BY author_channel_id)
		ORDER BY said_at DESC
		LIMIT ?`, guildID, "%"+likeEscaper.Replace(query)+"%", limit)
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (s *Store) queryLines(ctx context.Context, query string, args ...any) ([]store.Line, error) {
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []store.Line
	for rows.Next() {
		l, err := scanLine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}

	return out, rows.Err()
}

func scanLine(row scanner) (*store.Line, error) {
	var (
		l                       store.Line
		guild, channel, message sql.NullString
		kind                    string
		saidAt                  int64
	)
	if err := row.Scan(&l.VideoID, &guild, &channel, &message, &l.AuthorChannelID, &l.AuthorName,
		&l.Body, &kind, &saidAt); err != nil {
		return nil, err
	}

	l.GuildID, l.ChannelID, l.MessageID = guild.String, channel.String, message.String
	l.Kind = store.LineKind(kind)
	l.SaidAt = time.UnixMilli(saidAt)
	return &l, nil
}

func (s *Store) VideoChannels(ctx context.Context, videoID string) ([]store.VideoChannel, error) {
	rows, err := s.read.QueryContext(ctx, `
		SELECT DISTINCT guild_id, discord_channel_id FROM relayed_lines
		WHERE video_id = ? AND guild_id IS NOT NULL AND discord_channel_id IS NOT NULL
			AND kind IN ('owner', 'tl', 'vtuber', 'mod')
		ORDER BY guild_id, discord_channel_id`, videoID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []store.VideoChannel
	for rows.Next() {
		var vc store.VideoChannel
		if err := rows.Scan(&vc.GuildID, &vc.ChannelID); err != nil {
			return nil, err
		}
		out = append(out, vc)
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

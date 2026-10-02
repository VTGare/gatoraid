package sqlite

import (
	"context"
	"time"

	"github.com/VTGare/gatoraid/store"
)

func (s *Store) ClaimNotice(ctx context.Context, n store.Notice) (bool, error) {
	r, err := s.write.ExecContext(ctx, `
		INSERT INTO stream_notices (guild_id, video_id, kind, discord_channel_id, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		n.GuildID, n.VideoID, string(n.Kind), n.ChannelID, time.Now().UnixMilli())
	if err != nil {
		return false, err
	}

	created, err := r.RowsAffected()
	return created > 0, err
}

func (s *Store) SetNoticeMessage(ctx context.Context, n store.Notice, messageID string) error {
	_, err := s.write.ExecContext(ctx, `
		UPDATE stream_notices SET discord_message_id = ?
		WHERE guild_id = ? AND video_id = ? AND kind = ? AND discord_channel_id = ?`,
		messageID, n.GuildID, n.VideoID, string(n.Kind), n.ChannelID)
	return err
}

func (s *Store) PruneNotices(ctx context.Context, before time.Time) (int, error) {
	r, err := s.write.ExecContext(ctx, `DELETE FROM stream_notices WHERE created_at < ?`, before.UnixMilli())
	if err != nil {
		return 0, err
	}

	n, err := r.RowsAffected()
	return int(n), err
}

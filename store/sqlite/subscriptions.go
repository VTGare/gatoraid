package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/VTGare/gatoraid/store"
)

const subscriptionColumns = `s.id, s.guild_id, s.feature, s.target_kind, s.target, s.discord_channel_id,
	s.role_id, s.created_by, s.created_at`

func (s *Store) Subscriptions(ctx context.Context) ([]store.Subscription, error) {
	return s.querySubscriptions(ctx, `
		SELECT `+subscriptionColumns+` FROM subscriptions s
		JOIN guilds g ON g.id = s.guild_id
		WHERE g.left_at IS NULL
		ORDER BY s.id`)
}

func (s *Store) GuildSubscriptions(ctx context.Context, guildID string, feature store.Feature) ([]store.Subscription, error) {
	return s.querySubscriptions(ctx, `
		SELECT `+subscriptionColumns+` FROM subscriptions s
		WHERE s.guild_id = ? AND s.feature = ?
		ORDER BY s.discord_channel_id, s.id`, guildID, string(feature))
}

func (s *Store) AddSubscription(ctx context.Context, sub store.Subscription) (bool, error) {
	var created bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var exists bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM guilds WHERE id = ?)`, sub.GuildID).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return store.ErrGuildNotFound
		}

		r, err := tx.ExecContext(ctx, `
			UPDATE subscriptions SET role_id = ?
			WHERE guild_id = ? AND feature = ? AND target_kind = ? AND target = ? AND discord_channel_id = ?`,
			nullString(sub.RoleID), sub.GuildID, string(sub.Feature), string(sub.Target.Kind), sub.Target.ID, sub.ChannelID)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err != nil || n > 0 {
			return err
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO subscriptions (guild_id, feature, target_kind, target, discord_channel_id, role_id,
				created_by, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			sub.GuildID, string(sub.Feature), string(sub.Target.Kind), sub.Target.ID, sub.ChannelID,
			nullString(sub.RoleID), sub.CreatedBy, time.Now().UnixMilli())
		created = err == nil
		return err
	})

	return created, err
}

func (s *Store) RemoveSubscription(ctx context.Context, guildID string, feature store.Feature, target store.Target, channelID string) error {
	r, err := s.write.ExecContext(ctx, `
		DELETE FROM subscriptions
		WHERE guild_id = ? AND feature = ? AND target_kind = ? AND target = ? AND discord_channel_id = ?`,
		guildID, string(feature), string(target.Kind), target.ID, channelID)
	if err != nil {
		return err
	}

	if n, err := r.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return store.ErrSubscriptionNotFound
	}

	return nil
}

func (s *Store) ClearSubscriptions(ctx context.Context, guildID string, feature store.Feature, channelID string) (int, error) {
	r, err := s.write.ExecContext(ctx,
		`DELETE FROM subscriptions WHERE guild_id = ? AND feature = ? AND discord_channel_id = ?`,
		guildID, string(feature), channelID)
	if err != nil {
		return 0, err
	}

	n, err := r.RowsAffected()
	return int(n), err
}

func (s *Store) querySubscriptions(ctx context.Context, query string, args ...any) ([]store.Subscription, error) {
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []store.Subscription
	for rows.Next() {
		var (
			sub           store.Subscription
			feature, kind string
			role          sql.NullString
			createdAt     int64
		)
		if err := rows.Scan(&sub.ID, &sub.GuildID, &feature, &kind, &sub.Target.ID, &sub.ChannelID,
			&role, &sub.CreatedBy, &createdAt); err != nil {
			return nil, err
		}

		sub.Feature = store.Feature(feature)
		sub.Target.Kind = store.TargetKind(kind)
		sub.RoleID = role.String
		sub.CreatedAt = time.UnixMilli(createdAt)
		out = append(out, sub)
	}

	return out, rows.Err()
}

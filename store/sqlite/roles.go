package sqlite

import (
	"context"
	"database/sql"

	"github.com/VTGare/gatoraid/store"
)

func (s *Store) GuildRoles(ctx context.Context, guildID string, kind store.RoleKind) ([]string, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT role_id FROM guild_roles WHERE guild_id = ? AND kind = ? ORDER BY role_id`, guildID, string(kind))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}

	return out, rows.Err()
}

func (s *Store) SetGuildRoles(ctx context.Context, guildID string, kind store.RoleKind, roleIDs []string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM guilds WHERE id = ?)`, guildID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return store.ErrGuildNotFound
		}

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM guild_roles WHERE guild_id = ? AND kind = ?`, guildID, string(kind)); err != nil {
			return err
		}

		for _, id := range roleIDs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO guild_roles (guild_id, role_id, kind) VALUES (?, ?, ?)
				ON CONFLICT DO NOTHING`, guildID, id, string(kind)); err != nil {
				return err
			}
		}
		return nil
	})
}

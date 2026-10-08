-- Lowercase Twitch username, empty for streamers without one.
ALTER TABLE streamers ADD COLUMN twitch TEXT NOT NULL DEFAULT '';

CREATE INDEX streamers_twitch ON streamers (twitch) WHERE twitch != '';

-- SQLite can't change a CHECK constraint, so the table is copied into one
-- that also allows 'twitch'.
CREATE TABLE subscriptions_new (
    id                 INTEGER PRIMARY KEY,
    guild_id           TEXT    NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    feature            TEXT    NOT NULL CHECK (feature IN ('relay', 'cameos', 'gossip', 'youtube', 'twitch', 'posts')),
    target_kind        TEXT    NOT NULL CHECK (target_kind IN ('channel', 'group', 'all')),
    target             TEXT    NOT NULL DEFAULT '',
    discord_channel_id TEXT    NOT NULL,
    role_id            TEXT,
    created_by         TEXT    NOT NULL,
    created_at         INTEGER NOT NULL,
    UNIQUE (guild_id, feature, target_kind, target, discord_channel_id)
) STRICT;

INSERT INTO subscriptions_new (id, guild_id, feature, target_kind, target, discord_channel_id, role_id, created_by, created_at)
SELECT id, guild_id, feature, target_kind, target, discord_channel_id, role_id, created_by, created_at FROM subscriptions;

DROP TABLE subscriptions;
ALTER TABLE subscriptions_new RENAME TO subscriptions;

CREATE INDEX subscriptions_target ON subscriptions (target_kind, target);

-- target is a YouTube channel ID or a group ID, and empty for 'all'. Channel
-- targets don't reference streamers, so hiding or purging a streamer never
-- touches a guild's subscriptions.
CREATE TABLE subscriptions (
    id                 INTEGER PRIMARY KEY,
    guild_id           TEXT    NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    feature            TEXT    NOT NULL CHECK (feature IN ('relay', 'cameos', 'gossip', 'youtube', 'posts')),
    target_kind        TEXT    NOT NULL CHECK (target_kind IN ('channel', 'group', 'all')),
    target             TEXT    NOT NULL DEFAULT '',
    discord_channel_id TEXT    NOT NULL,
    role_id            TEXT,
    created_by         TEXT    NOT NULL,
    created_at         INTEGER NOT NULL,
    UNIQUE (guild_id, feature, target_kind, target, discord_channel_id)
) STRICT;

CREATE INDEX subscriptions_target ON subscriptions (target_kind, target);

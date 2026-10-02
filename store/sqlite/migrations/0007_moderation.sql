CREATE TABLE blacklist (
    guild_id      TEXT    NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    yt_channel_id TEXT    NOT NULL,
    name          TEXT    NOT NULL DEFAULT '',
    reason        TEXT    NOT NULL DEFAULT '',
    added_by      TEXT    NOT NULL,
    created_at    INTEGER NOT NULL,
    PRIMARY KEY (guild_id, yt_channel_id)
) STRICT, WITHOUT ROWID;

-- Patterns are stored lowercase.
CREATE TABLE filters (
    guild_id   TEXT    NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL CHECK (kind IN ('banned', 'wanted')),
    pattern    TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (guild_id, kind, pattern)
) STRICT, WITHOUT ROWID;

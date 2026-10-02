-- Timestamps are Unix milliseconds.

CREATE TABLE guilds (
    id        TEXT    PRIMARY KEY,
    settings  TEXT    NOT NULL DEFAULT '{}',
    joined_at INTEGER NOT NULL,
    left_at   INTEGER
) STRICT;

CREATE INDEX guilds_left_at ON guilds (left_at) WHERE left_at IS NOT NULL;

CREATE TABLE guild_roles (
    guild_id TEXT NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    role_id  TEXT NOT NULL,
    kind     TEXT NOT NULL CHECK (kind IN ('manager', 'blacklister')),
    PRIMARY KEY (guild_id, role_id, kind)
) STRICT, WITHOUT ROWID;

CREATE TABLE streamer_groups (
    id                  TEXT    PRIMARY KEY,
    name                TEXT    NOT NULL,
    parent_id           TEXT    REFERENCES streamer_groups (id) ON DELETE SET NULL,
    skip_auto_translate INTEGER NOT NULL DEFAULT 0,
    position            INTEGER NOT NULL DEFAULT 0
) STRICT;

-- removed_at is set while a streamer is hidden. The row stays, so
-- subscriptions to it survive and come back if the streamer does.
CREATE TABLE streamers (
    channel_id        TEXT    PRIMARY KEY,
    name              TEXT    NOT NULL,
    channel_name      TEXT    NOT NULL DEFAULT '',
    group_id          TEXT    REFERENCES streamer_groups (id) ON DELETE SET NULL,
    twitter           TEXT    NOT NULL DEFAULT '',
    aliases           TEXT    NOT NULL DEFAULT '[]',
    avatar_url        TEXT    NOT NULL DEFAULT '',
    free_chat_streams INTEGER NOT NULL DEFAULT 0,
    source            TEXT    NOT NULL CHECK (source IN ('seed', 'owner', 'user')),
    added_by_guild    TEXT,
    updated_at        INTEGER NOT NULL,
    removed_at        INTEGER
) STRICT;

CREATE INDEX streamers_group_id ON streamers (group_id);

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

-- Notices already sent, so restarts and repeated tracker events don't post
-- or ping twice. Rows are claimed before sending.
CREATE TABLE stream_notices (
    guild_id           TEXT    NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    video_id           TEXT    NOT NULL,
    kind               TEXT    NOT NULL CHECK (kind IN ('prechat', 'relay', 'live')),
    discord_channel_id TEXT    NOT NULL,
    discord_message_id TEXT,
    created_at         INTEGER NOT NULL,
    PRIMARY KEY (guild_id, video_id, kind, discord_channel_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX stream_notices_created_at ON stream_notices (created_at);

-- Lines the bot relayed, for TL logs and for blacklisting the author of a
-- relayed message. guild_id is NULL for the bot-wide archive, which keeps
-- what every chat it read would have relayed.
CREATE TABLE relayed_lines (
    id                 INTEGER PRIMARY KEY,
    video_id           TEXT    NOT NULL,
    guild_id           TEXT    REFERENCES guilds (id) ON DELETE CASCADE,
    discord_channel_id TEXT,
    discord_message_id TEXT,
    author_channel_id  TEXT    NOT NULL,
    author_name        TEXT    NOT NULL,
    body               TEXT    NOT NULL,
    kind               TEXT    NOT NULL CHECK (kind IN ('owner', 'tl', 'vtuber', 'mod', 'cameo', 'gossip')),
    said_at            INTEGER NOT NULL
) STRICT;

CREATE INDEX relayed_lines_message ON relayed_lines (guild_id, discord_message_id)
    WHERE discord_message_id IS NOT NULL;
CREATE INDEX relayed_lines_video ON relayed_lines (video_id, guild_id);
CREATE INDEX relayed_lines_said_at ON relayed_lines (said_at);

-- End-of-stream logs already posted. Rows are claimed before posting.
CREATE TABLE logs_posted (
    guild_id           TEXT    NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    video_id           TEXT    NOT NULL,
    discord_channel_id TEXT    NOT NULL,
    posted_at          INTEGER NOT NULL,
    PRIMARY KEY (guild_id, video_id, discord_channel_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX logs_posted_posted_at ON logs_posted (posted_at);

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

-- Keys of things already handled, like community posts. seen_at is
-- refreshed every time a key is seen, so only keys that stopped turning up
-- get pruned.
CREATE TABLE dedupe (
    kind    TEXT    NOT NULL,
    key     TEXT    NOT NULL,
    seen_at INTEGER NOT NULL,
    PRIMARY KEY (kind, key)
) STRICT, WITHOUT ROWID;

CREATE INDEX dedupe_seen_at ON dedupe (seen_at);

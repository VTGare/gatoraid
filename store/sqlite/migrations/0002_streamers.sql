CREATE TABLE streamer_groups (
    id                  TEXT    PRIMARY KEY,
    name                TEXT    NOT NULL,
    parent_id           TEXT    REFERENCES streamer_groups (id) ON DELETE SET NULL,
    skip_auto_translate INTEGER NOT NULL DEFAULT 0,
    position            INTEGER NOT NULL DEFAULT 0
) STRICT;

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
    updated_at        INTEGER NOT NULL
) STRICT;

CREATE INDEX streamers_group_id ON streamers (group_id);

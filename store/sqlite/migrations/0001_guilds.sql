-- Timestamps are Unix milliseconds.
CREATE TABLE guilds (
    id        TEXT    PRIMARY KEY,
    settings  TEXT    NOT NULL DEFAULT '{}',
    joined_at INTEGER NOT NULL,
    left_at   INTEGER
) STRICT;

CREATE INDEX guilds_left_at ON guilds (left_at) WHERE left_at IS NOT NULL;

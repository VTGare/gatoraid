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

-- End-of-stream logs already posted. Rows are claimed before posting.
CREATE TABLE logs_posted (
    guild_id           TEXT    NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    video_id           TEXT    NOT NULL,
    discord_channel_id TEXT    NOT NULL,
    posted_at          INTEGER NOT NULL,
    PRIMARY KEY (guild_id, video_id, discord_channel_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX logs_posted_posted_at ON logs_posted (posted_at);

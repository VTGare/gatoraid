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

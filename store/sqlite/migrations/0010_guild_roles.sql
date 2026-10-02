CREATE TABLE guild_roles (
    guild_id TEXT NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    role_id  TEXT NOT NULL,
    kind     TEXT NOT NULL CHECK (kind IN ('manager', 'blacklister')),
    PRIMARY KEY (guild_id, role_id, kind)
) STRICT, WITHOUT ROWID;

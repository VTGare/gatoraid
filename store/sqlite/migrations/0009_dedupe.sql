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

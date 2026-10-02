-- Set while a streamer is hidden. The row stays, so subscriptions to it
-- survive and come back if the streamer does.
ALTER TABLE streamers ADD COLUMN removed_at INTEGER;

-- Per-user setlist visibility. Setlists are spoilers for anyone who would
-- rather hear the show cold, so this is opt-in: every user starts at FALSE and
-- the event detail page obfuscates the setlist until they turn it on.
ALTER TABLE users ADD COLUMN show_setlists BOOLEAN NOT NULL DEFAULT FALSE;

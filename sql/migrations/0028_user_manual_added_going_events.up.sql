-- Shows the user typed in themselves, purely so they show up in the calendar
-- page's Upcoming/Past sidebar. Deliberately NOT part of the event pipeline:
-- no FK to events, no venue_id, no match row, no scraper ever writes here. A
-- row is three strings the user remembered about a gig, owned by that user.
--
-- show_date is DATE, not TIMESTAMPTZ, because the form asks only for a day --
-- nobody recalls the doors time of a show they saw last spring. The client
-- reads it as a LOCAL date and treats the show as past once that whole day is
-- over, so a gig tonight stays under "Upcoming" instead of moving at UTC
-- midnight.
CREATE TABLE user_manual_added_going_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    show_date  DATE NOT NULL,
    event_name TEXT NOT NULL,
    venue_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Postgres does not index a foreign key automatically, and every ON DELETE
-- CASCADE from users would otherwise seq-scan this table. Also the access
-- path for the list endpoint, which only ever reads one user's rows.
CREATE INDEX user_manual_added_going_events_user_id_idx
    ON user_manual_added_going_events (user_id);

CREATE TABLE match_config (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO match_config (key, value) VALUES
    ('w_string',        '0.6'::jsonb),
    ('w_embedding',     '0.4'::jsonb),
    ('score_threshold', '0.3'::jsonb),
    ('artist_factor',   '1.0'::jsonb),
    ('genre_factor',    '0.3'::jsonb),
    ('string_max',      '3.0'::jsonb);

-- Per-user match-score threshold. NULL means "use the global default from
-- match_config (score_threshold)". Stored as a plain float in [0,1]; the API
-- clamps user-set values to [0.20, 0.60].
ALTER TABLE users ADD COLUMN score_threshold DOUBLE PRECISION;

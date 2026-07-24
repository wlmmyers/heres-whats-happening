DROP TABLE IF EXISTS email_confirmations;
ALTER TABLE users DROP COLUMN IF EXISTS confirmed;

CREATE TABLE users (
    id                            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email                         TEXT NOT NULL,
    password_hash                 TEXT NOT NULL,
    city_id                       UUID NOT NULL REFERENCES cities(id),
    interest_embedding            vector(384),
    interest_embedding_updated_at TIMESTAMPTZ,
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at                    TIMESTAMPTZ
);

CREATE UNIQUE INDEX users_email_active
    ON users (email)
    WHERE deleted_at IS NULL;

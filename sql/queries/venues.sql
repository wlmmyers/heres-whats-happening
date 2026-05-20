-- name: UpsertVenue :one
INSERT INTO venues (city_id, name, normalized_name, address, lat, lng, website_url)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (city_id, normalized_name)
DO UPDATE SET
    name        = EXCLUDED.name,
    address     = EXCLUDED.address,
    lat         = EXCLUDED.lat,
    lng         = EXCLUDED.lng,
    website_url = EXCLUDED.website_url,
    updated_at  = NOW()
RETURNING id;

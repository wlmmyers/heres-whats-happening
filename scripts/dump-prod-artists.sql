-- Stage 1: extract prod's artist enrichment as TSV. READ ONLY — writes nothing.
--
-- Child tables are exported keyed by artists.name_key, NOT artist_id, so the
-- load never depends on prod and local agreeing on a UUID. name_key is the
-- natural key the whole enrichment key space already uses (see
-- 0024_artist_enrichment.up.sql).
--
-- Output goes to files via \copy (client-side), one per table.

\copy (SELECT id, name_key, display_name, mbid, disambiguation, artist_type, country, begin_year, status, resolved_at, created_at, updated_at FROM artists ORDER BY name_key) TO 'artists.tsv'

\copy (SELECT a.name_key, i.status, i.url, i.width, i.height, i.file, i.source, i.credit, i.reason, i.checked_at FROM artist_images i JOIN artists a ON a.id = i.artist_id ORDER BY a.name_key) TO 'artist_images.tsv'

\copy (SELECT a.name_key, b.status, b.bio_md, b.sources, b.model, b.reason, b.generated_at FROM artist_bios b JOIN artists a ON a.id = b.artist_id ORDER BY a.name_key) TO 'artist_bios.tsv'

\copy (SELECT a.name_key, t.status, t.tour_name, t.songs, t.observed_date, t.observed_venue, t.observed_city, t.setlist_url, t.blurb, t.blurb_model, t.reason, t.fetched_at FROM artist_tour_snapshots t JOIN artists a ON a.id = t.artist_id ORDER BY a.name_key) TO 'artist_tour_snapshots.tsv'

-- Which performer prod picked as each event's headliner, addressed by the pair
-- events is UNIQUE on. Local event UUIDs differ from prod's, so this is the only
-- way to carry the link across.
\copy (SELECT s.name, e.source_event_id, a.name_key FROM events e JOIN event_sources s ON s.id = e.source_id JOIN artists a ON a.id = e.headline_artist_id WHERE e.headline_artist_id IS NOT NULL ORDER BY s.name, e.source_event_id) TO 'headline_links.tsv'

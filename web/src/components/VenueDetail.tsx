import type { Venue } from '../api/venues';
import ExternalLink from './ExternalLink';
import * as s from './VenueDetail.css';

const nextEventFormat = new Intl.DateTimeFormat(undefined, {
  weekday: 'short',
  month: 'short',
  day: 'numeric',
});

// A maps search on name + address finds the venue itself rather than a bare
// street address; coordinates are the fallback when no address was scraped.
function mapsUrl(venue: Venue): string | undefined {
  let query: string;
  if (venue.address) query = `${venue.name}, ${venue.address}`;
  else if (venue.lat != null && venue.lng != null) query = `${venue.lat},${venue.lng}`;
  else return undefined;
  return `https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(query)}`;
}

// The bare host reads better than a full URL with scheme and path.
function displayHost(url: string): string {
  try {
    return new URL(url).host.replace(/^www\./, '');
  } catch {
    return url;
  }
}

export default function VenueDetail({ venue }: { venue: Venue }) {
  const count = venue.upcoming_event_count;
  const map = mapsUrl(venue);

  return (
    <article className={s.venueDetail}>
      <div className={s.main}>
        <h2 className={s.name}>{venue.name}</h2>
        {venue.address && <p className={s.address}>{venue.address}</p>}
        {(map || venue.website_url) && (
          <div className={s.links}>
            {map && <ExternalLink href={map}>Map</ExternalLink>}
            {venue.website_url && (
              <ExternalLink href={venue.website_url}>{displayHost(venue.website_url)}</ExternalLink>
            )}
          </div>
        )}
      </div>
      <div className={s.stats}>
        <span className={s.count}>{count}</span>
        <span className={s.countLabel}>upcoming shows</span>
        <span className={s.next}>
          Next: {nextEventFormat.format(new Date(venue.next_event_at))}
        </span>
      </div>
    </article>
  );
}

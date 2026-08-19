import type { CalendarEvent } from '../api/calendar';

// The venue's own image wins, but its credit does not: the Commons credit
// describes the artist image only, and attributing it to a promo shot the
// venue supplied would be a false attribution.
export const getArtistImage = (event: CalendarEvent) =>
  event.image_url
    ? { url: event.image_url, credit: undefined }
    : { url: event.artist?.image?.url, credit: event.artist?.image?.credit };

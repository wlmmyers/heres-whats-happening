import { useSearchParams } from 'react-router-dom';
import HorizontalSelector from '../components/HorizontalSelector';
import Layout from '../components/Layout';
import SectionTitle from '../components/SectionTitle';
import { Skeleton } from '../components/Skeleton';
import SkeletonCard from '../components/SkeletonCard';
import VenueDetail from '../components/VenueDetail';
import { VenueEvents } from '../components/VenueEvents';
import { useAuth } from '../auth/useAuth';
import { useCityVenues } from '../hooks/useCityVenues';
import * as c from '../styles/common.css';
import * as s from './VenuesPage.css';

export default function VenuesPage() {
  const { user } = useAuth();
  const { data: venues = [], isLoading, isError } = useCityVenues(user?.city_id);

  // The selection lives in the URL so a refresh, a shared link, or Back from an
  // event detail page all land on the same venue. Unknown or absent ids fall
  // back to the first venue.
  const [params, setParams] = useSearchParams();
  const selected = venues.find((v) => v.id === params.get('venue')) ?? venues[0];
  const select = (id: string) => {
    const next = new URLSearchParams(params);
    next.set('venue', id);
    setParams(next, { replace: true });
  };

  return (
    <Layout>
      <div className={c.pageHeader}>
        <h1 className={c.pageTitle}>Seattle Venues</h1>
        <p className={c.pageHeaderAside}>Sorted by event count</p>
      </div>
      {isLoading ? (
        <>
          <div className={s.selectorSkeleton}>
            <Skeleton type="fill" />
          </div>
          <SkeletonCard height={140} />
        </>
      ) : isError ? (
        <div className={s.errorBox}>Couldn't load venues, please try again.</div>
      ) : !selected ? (
        <div className={s.emptyState}>No Seattle venues have upcoming shows right now.</div>
      ) : (
        <>
          <div className={s.selectorContainer}>
            <HorizontalSelector
              wrap
              className={s.horizontalSelector}
              aria-label="Venues"
              items={venues.map((v) => ({
                key: v.id,
                content: (
                  <span className={s.selectorItem}>
                    <span className={s.selectorName} title={v.name}>
                      {v.name}
                    </span>
                    <span className={s.selectorCount}>{v.upcoming_event_count}</span>
                  </span>
                ),
              }))}
              activeKey={selected.id}
              onSelect={select}
            />
          </div>
          <VenueDetail venue={selected} />
          <div className={s.eventsSection}>
            <SectionTitle>Upcoming shows</SectionTitle>
            <VenueEvents cityId={user?.city_id} venueId={selected.id} venueName={selected.name} />
          </div>
        </>
      )}
    </Layout>
  );
}

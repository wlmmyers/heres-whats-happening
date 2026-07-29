import EventCard from '../components/EventCard';
import * as s from './CalendarPage.css';
import * as c from '../styles/common.css';
import loggedOutData from '../api/logged-out-calendar-data.json';
import type { CalendarEvent } from '../api/calendar';
import { useAuth } from '../auth/useAuth';
import { Navigate, useLocation } from 'react-router-dom';
import SkeletonCard from '../components/SkeletonCard';
import { useLayoutEffect, useState } from 'react';

export default function LandingPage({ children }: { children?: React.ReactNode }) {
  const { status } = useAuth();
  const location = useLocation();
  const data = loggedOutData as { events: CalendarEvent[] };
  const [spacerHeight, setSpacerHeight] = useState(0);

  // Auto-scroll the page down at a constant speed, pausing when the user interacts with the page.
  useLayoutEffect(() => {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;

    const SPEED = 45; // px per second
    const RESUME_DELAY = 3000; // ms of inactivity before auto-scroll resumes

    let rafId = 0;
    let resumeTimer: ReturnType<typeof setTimeout> | undefined;
    let paused = false;
    let lastTs = 0;
    let pos = window.scrollY;
    const RENDER_MORE_BUFFER = 200; // px from bottom of page to trigger more rendering

    const maxScroll = () => Math.max(0, document.documentElement.scrollHeight - window.innerHeight);

    const tick = (ts: number) => {
      if (lastTs === 0) lastTs = ts;
      const dt = ts - lastTs;
      lastTs = ts;

      if (!paused) {
        const max = maxScroll();
        if (max > window.innerHeight) {
          pos += (SPEED * dt) / 1000;
          window.scrollTo(0, pos);
          if (pos > max - RENDER_MORE_BUFFER) {
            setSpacerHeight((prevHeight) => prevHeight + (150 + 12) * loggedOutData.events.length); // 150px card height + 12px margin
          }
        }
      }
      rafId = requestAnimationFrame(tick);
    };

    // User interaction pauses the pan; after RESUME_DELAY of quiet we pick up
    // again from wherever they left the page.
    const pause = () => {
      paused = true;
      setSpacerHeight(0);
      clearTimeout(resumeTimer);
      resumeTimer = setTimeout(() => {
        pos = window.scrollY;
        lastTs = 0;
        paused = false;
      }, RESUME_DELAY);
    };

    const passive: AddEventListenerOptions = { passive: true };
    window.addEventListener('wheel', pause, passive);
    window.addEventListener('touchmove', pause, passive);

    rafId = requestAnimationFrame(tick);

    return () => {
      cancelAnimationFrame(rafId);
      clearTimeout(resumeTimer);
      window.removeEventListener('wheel', pause, passive);
      window.removeEventListener('touchmove', pause, passive);

      // reset scroll position to top when leaving the page, so that the next time the user visits the landing page they see the top of the list
      window.scrollTo(0, 0);
    };
  }, []);

  // The index path of the site renders this component with no children. In this case, redirect to
  // either the calendar or login page. The status === 'loading' case hits first and falls through to
  // the render, rendering the skeleton cards while the auth status is being determined.
  // location.search must survive: ?welcome=true / ?confirmerror=true arrive on
  // the index route, and Layout's modals read them. Dropping the params here
  // loses the modal before it can ever render.
  if (!children) {
    if (status === 'authenticated') {
      return <Navigate to={`/calendar${location.search}`} replace />;
    } else if (status === 'anonymous') {
      return <Navigate to={`/login${location.search}`} replace />;
    }
  }

  return (
    <div>
      <div className={c.pageHeader}>
        <h1 className={c.pageTitle}>Your Seattle calendar</h1>
      </div>
      {status === 'loading' ? (
        <ul className={s.list}>
          {Array.from({ length: 5 }, (_, i) => ({ id: `loading-${i}` })).map((e) => (
            <li key={e.id} className={s.listItem}>
              <SkeletonCard height={150} />
            </li>
          ))}
        </ul>
      ) : (
        <ul className={s.list}>
          <div style={{ height: spacerHeight }} /> {/* Spacer */}
          {[...data.events, ...data.events].map((e, i) => (
            <li key={`${e.id}-${i}`} className={s.listItem}>
              <EventCard event={e} interactive={false} onNotInterested={() => {}} />
            </li>
          ))}
        </ul>
      )}
      <div className={c.screen}>{children}</div>
    </div>
  );
}

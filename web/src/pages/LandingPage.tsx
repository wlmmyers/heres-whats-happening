import EventCard from '../components/EventCard';
import * as s from './CalendarPage.css';
import * as c from '../styles/common.css';
import loggedOutData from '../api/logged-out-calendar-data.json';
import type { CalendarEvent } from '../api/calendar';
import { useAuth } from '../auth/useAuth';
import { Navigate } from 'react-router-dom';
import SkeletonCard from '../components/SkeletonCard';
import { useLayoutEffect, useState } from 'react';

export default function LandingPage({ children }: { children?: React.ReactNode }) {
  const { status } = useAuth();
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

  // A logged-in visitor to the bare landing page belongs on their calendar.
  // When a login/signup dialog is mounted we defer to that dialog's own
  // post-auth navigation (login → calendar, signup → interests onboarding),
  // so we don't race it with a redirect here.
  if (status === 'authenticated' && !children) {
    return <Navigate to="/calendar" replace />;
  }

  return (
    <div>
      <div className={c.pageHeader}>
        <h1 className={c.pageTitle}>Your matched calendar</h1>
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

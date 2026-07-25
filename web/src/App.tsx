import { useEffect } from 'react';
import { Routes, Route, Navigate, useLocation } from 'react-router-dom';
import RequireAuth from './auth/RequireAuth';
import Layout from './components/Layout';
import InterestsPage from './pages/InterestsPage';
import CalendarPage from './pages/CalendarPage';
import EventDetailPage from './pages/EventDetailPage';
import SettingsPage from './pages/SettingsPage';
import SpotifyCallbackPage from './pages/SpotifyCallbackPage';
import LandingPage from './pages/LandingPage';
import LoginDialog from './components/LoginDialog';
import SignupDialog from './components/SignupDialog';

function ScrollToTop() {
  const { pathname } = useLocation();
  useEffect(() => {
    window.scrollTo(0, 0);
  }, [pathname]);
  return null;
}

export default function App() {
  return (
    <>
      <ScrollToTop />
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<LandingPage />} />
          <Route
            path="login"
            element={
              <LandingPage>
                <LoginDialog />
              </LandingPage>
            }
          />
          <Route
            path="signup"
            element={
              <LandingPage>
                <SignupDialog />
              </LandingPage>
            }
          />
          <Route
            path="calendar/seattle"
            element={
              <RequireAuth>
                <CalendarPage />
              </RequireAuth>
            }
          />
          <Route path="calendar" element={<Navigate to="/calendar/seattle" replace />} />
          <Route
            path="interests"
            element={
              <RequireAuth>
                <InterestsPage />
              </RequireAuth>
            }
          />
          <Route
            path="events/:id"
            element={
              <RequireAuth>
                <EventDetailPage />
              </RequireAuth>
            }
          />
          <Route
            path="settings"
            element={
              <RequireAuth>
                <SettingsPage />
              </RequireAuth>
            }
          />
          <Route
            path="integrations/spotify/callback"
            element={
              <RequireAuth>
                <SpotifyCallbackPage />
              </RequireAuth>
            }
          />
        </Route>
      </Routes>
    </>
  );
}

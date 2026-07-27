import { Routes, Route, Navigate, useLocation } from 'react-router-dom';
import RequireAuth from './auth/RequireAuth';
import Layout from './components/Layout';
import InterestsPage from './pages/InterestsPage';
import CalendarPage from './pages/CalendarPage';
import EventDetailPage from './pages/EventDetailPage';
import SettingsPage from './pages/SettingsPage';
import SpotifyCallbackPage from './pages/SpotifyCallbackPage';
import LandingPage from './pages/LandingPage';
import ConfirmEmailPage from './pages/ConfirmEmailPage';
import LoginDialog from './components/LoginDialog';
import SignupDialog from './components/SignupDialog';

// /calendar is a redirect to the only city we ship. It preserves the query
// string because ?welcome=true reaches it via LandingPage, and Layout's modal
// reads that param after the hop — a bare <Navigate> would drop it.
function CalendarRedirect() {
  const { search } = useLocation();
  return <Navigate to={`/calendar/seattle${search}`} replace />;
}

export default function App() {
  return (
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
        <Route path="calendar" element={<CalendarRedirect />} />
        <Route
          path="confirm-email"
          element={
            <RequireAuth allowUnconfirmed>
              <ConfirmEmailPage />
            </RequireAuth>
          }
        />
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
  );
}

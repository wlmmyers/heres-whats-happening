import { Routes, Route } from 'react-router-dom';
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

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Layout />}>
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
        <Route index element={<LandingPage />} />
        <Route
          path="calendar"
          element={
            <RequireAuth>
              <CalendarPage />
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

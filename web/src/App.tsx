import { Routes, Route, Navigate } from 'react-router-dom';
import RequireAuth from './auth/RequireAuth';
import Layout from './components/Layout';
import LoginPage from './pages/LoginPage';
import SignupPage from './pages/SignupPage';
import InterestsPage from './pages/InterestsPage';
import CalendarPage from './pages/CalendarPage';
import EventDetailPage from './pages/EventDetailPage';
import SettingsPage from './pages/SettingsPage';
import SpotifyCallbackPage from './pages/SpotifyCallbackPage';

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/signup" element={<SignupPage />} />
      <Route path="/" element={<Layout />}>
        <Route index element={<Navigate to="/calendar" replace />} />
        <Route path="calendar" element={<CalendarPage />} />
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

import { Routes, Route, Navigate } from 'react-router-dom';
import RequireAuth from './auth/RequireAuth';
import Layout from './components/Layout';
import LoginPage from './pages/LoginPage';
import SignupPage from './pages/SignupPage';
import OnboardingPage from './pages/OnboardingPage';
import CalendarPage from './pages/CalendarPage';
import EventDetailPage from './pages/EventDetailPage';
import SettingsPage from './pages/SettingsPage';
import SpotifyCallbackPage from './pages/SpotifyCallbackPage';

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/signup" element={<SignupPage />} />
      <Route
        path="/"
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<Navigate to="/calendar" replace />} />
        <Route path="onboarding" element={<OnboardingPage />} />
        <Route path="calendar" element={<CalendarPage />} />
        <Route path="events/:id" element={<EventDetailPage />} />
        <Route path="settings" element={<SettingsPage />} />
        <Route path="integrations/spotify/callback" element={<SpotifyCallbackPage />} />
      </Route>
    </Routes>
  );
}

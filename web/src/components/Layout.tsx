import { NavLink, Outlet } from 'react-router-dom';
import UserMenu from './UserMenu';

const link = ({ isActive }: { isActive: boolean }) =>
  `px-3 py-2 rounded ${isActive ? 'bg-blue-100 text-blue-800' : 'text-gray-700 hover:bg-gray-100'}`;

export default function Layout() {
  return (
    <div className="min-h-screen bg-gray-50">
      <nav className="bg-white border-b px-4 py-3 flex items-center gap-2">
        <NavLink to="/calendar" className={link}>Calendar</NavLink>
        <NavLink to="/onboarding" className={link}>Interests</NavLink>
        <NavLink to="/settings" className={link}>Settings</NavLink>
        <UserMenu />
      </nav>
      <main className="max-w-5xl mx-auto px-4 py-6">
        <Outlet />
      </main>
    </div>
  );
}

import { useEffect, useState, type ReactNode } from 'react';
import * as authApi from '../api/auth';
import { refreshSession } from '../api/client';
import type { User } from '../api/auth';
import { AuthContext, type AuthStatus } from './context';

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [status, setStatus] = useState<AuthStatus>('loading');

  useEffect(() => {
    let cancelled = false;
    authApi
      .getMe()
      .then((u) => {
        if (cancelled) return;
        setUser(u);
        setStatus('authenticated');
      })
      .catch(() => {
        if (cancelled) return;
        setStatus('anonymous');
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const login = async (email: string, password: string) => {
    await authApi.login(email, password);
    const u = await authApi.getMe();
    setUser(u);
    setStatus('authenticated');
  };

  const signup = async (email: string, password: string) => {
    const u = await authApi.signup(email, password);
    setUser(u);
    setStatus('authenticated');
  };

  const logout = async () => {
    await authApi.logout();
    setUser(null);
    setStatus('anonymous');
  };

  // Mints a fresh access token before re-reading /me, so a confirmation that
  // happened on another device is visible without waiting out the access TTL.
  const refreshUser = async () => {
    await refreshSession();
    const u = await authApi.getMe();
    setUser(u);
    setStatus('authenticated');
    return u;
  };

  return (
    <AuthContext.Provider value={{ user, status, login, signup, logout, refreshUser }}>
      {children}
    </AuthContext.Provider>
  );
}

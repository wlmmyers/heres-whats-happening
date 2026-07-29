import { createContext } from 'react';
import type { User } from '../api/auth';

export type AuthStatus = 'loading' | 'authenticated' | 'anonymous';

export interface AuthState {
  user: User | null;
  status: AuthStatus;
  login: (email: string, password: string) => Promise<void>;
  signup: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  /**
   * refreshUser mints a fresh access token and re-reads /me. ConfirmEmailPage
   * uses it to notice a confirmation that happened on another device without
   * waiting out the access-token TTL.
   */
  refreshUser: () => Promise<User>;
}

export const AuthContext = createContext<AuthState | undefined>(undefined);

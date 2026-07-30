import { Navigate, useLocation } from 'react-router-dom';
import type { ReactElement } from 'react';
import { useAuth } from './useAuth';

/**
 * RequireAuth gates a route on being signed in and — unless allowUnconfirmed is
 * set — on having confirmed the account's email address. One place covers both
 * the post-login and the page-reload cases.
 *
 * This is a convenience redirect, not the security boundary: the API rejects
 * unconfirmed users itself (middleware.RequireConfirmed), so a hand-edited
 * client cannot use it to get past the gate.
 */
export default function RequireAuth({
  children,
  allowUnconfirmed = false,
}: {
  children: ReactElement;
  allowUnconfirmed?: boolean;
}) {
  const { status, user } = useAuth();
  const location = useLocation();
  if (status === 'anonymous') {
    return <Navigate to="/login" replace state={{ from: location }} />;
  }
  if (!allowUnconfirmed && user && !user.confirmed) {
    return <Navigate to="/confirm-email" replace />;
  }
  return children;
}

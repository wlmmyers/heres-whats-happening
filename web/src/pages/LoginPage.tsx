import { useNavigate, useLocation } from 'react-router-dom';
import LoginForm from '../components/LoginForm';
import * as s from './LoginPage.css';

interface LocationState {
  from?: { pathname?: string };
}

export default function LoginPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const dest = (location.state as LocationState | null)?.from?.pathname ?? '/calendar';

  return (
    <div className={s.page}>
      <LoginForm onSuccess={() => navigate(dest, { replace: true })} />
    </div>
  );
}

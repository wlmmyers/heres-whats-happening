import { useNavigate } from 'react-router-dom';
import LoginForm from './LoginForm';
import * as s from './LoginDialog.css';

export default function LoginDialog() {
  const navigate = useNavigate();
  return (
    <div role="dialog" aria-label="Sign in" className={s.wrapper}>
      <LoginForm onSuccess={() => navigate('/calendar/seattle')} />
    </div>
  );
}

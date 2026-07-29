import { useNavigate } from 'react-router-dom';
import * as s from './SignupDialog.css';
import SignupForm from './SignupForm';

export default function SignupDialog() {
  const navigate = useNavigate();
  return (
    <div role="dialog" aria-label="Sign up" className={s.wrapper}>
      {/* A new account is unconfirmed, so it lands on /confirm-email rather
          than in the product. */}
      <SignupForm onSuccess={() => navigate('/confirm-email')} />
    </div>
  );
}

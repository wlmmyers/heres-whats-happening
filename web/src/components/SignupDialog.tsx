import { useNavigate } from 'react-router-dom';
import * as s from './SignupDialog.css';
import SignupForm from './SignupForm';

export default function SignupDialog() {
  const navigate = useNavigate();
  return (
    <div role="dialog" aria-label="Sign up" className={s.wrapper}>
      <SignupForm onSuccess={() => navigate('/interests')} />
    </div>
  );
}

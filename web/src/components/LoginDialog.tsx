import LoginForm from './LoginForm';
import * as s from './LoginDialog.css';

export default function LoginDialog() {
  return (
    <div role="dialog" aria-label="Sign in" className={s.wrapper}>
      <LoginForm />
    </div>
  );
}

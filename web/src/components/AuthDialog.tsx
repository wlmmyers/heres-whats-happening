import React from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import RotatingLogo from './RotatingLogo';
import * as s from './AuthDialog.css';
import * as c from '../styles/common.css';

export default function AuthDialog({
  formContent,
  ariaLabel,
}: {
  formContent: React.ReactNode;
  ariaLabel: string;
}) {
  const [params] = useSearchParams();
  const isWelcome = params.get('welcome') === 'true';

  return (
    <div role="dialog" aria-label={ariaLabel} className={s.wrapper}>
      <div className={s.authCard}>
        <RotatingLogo />
        <div className={s.subtitle}>
          {isWelcome ? (
            <>
              Your email is confirmed.
              <br /> Sign in to add your interests and get started!
            </>
          ) : (
            <>
              An AI-backed event calendar based on you
              <aside className={s.aside}>Currently only supporting the Seattle area</aside>
            </>
          )}
        </div>
        {formContent}
        <p className={s.aboutLinkSection}>
          <Link to="/about" className={c.link}>
            See how it works {`>`}
          </Link>
        </p>
      </div>
    </div>
  );
}

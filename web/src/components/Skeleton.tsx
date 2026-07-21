import type { CSSProperties, ReactNode } from 'react';
import classNames from 'clsx';

import * as styles from './Skeleton.css';

type SkeletonTypes = 'button' | 'fill' | 'input' | 'h1' | 'text' | 'fillInline';
export interface SkeletonProps extends CSSProperties {
  dataHcName?: string;
  className?: string;
  repeat?: number;
  children?: ReactNode;
  type?: SkeletonTypes;
  style?: CSSProperties;
  theme?: {
    Animation?: string;
  };
}
export const Skeleton = ({
  dataHcName,
  className,
  repeat = 1,
  children,
  type = 'fill',
  style = {},
  theme,
  ...styleProps
}: SkeletonProps) => {
  return (
    <>
      {[...Array(repeat).keys()].map((id) => (
        <div
          key={id}
          data-hc-name={dataHcName}
          className={classNames(styles.Skeleton, styles[type], className)}
          style={{ ...style, ...styleProps }}
        >
          <div className={classNames(styles.Animation, theme?.Animation)}>
            <span className={styles.Children}>
              {type === 'text' ? <>&nbsp;</> : null}
              {children}
            </span>
          </div>
        </div>
      ))}
    </>
  );
};

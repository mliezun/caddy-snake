import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

const FeatureList = [
  {
    title: 'Python inside Caddy',
    description: (
      <>
        Serve WSGI, ASGI, and ESGI apps without a separate app server. Caddy
        terminates TLS and talks to Python workers over a local socket.
      </>
    ),
  },
  {
    title: 'Automatic HTTPS',
    description: (
      <>
        Same Caddy certificates you already know — including on-demand TLS for
        wildcard preview hosts gated by a directory on disk.
      </>
    ),
  },
  {
    title: 'One process, many apps',
    description: (
      <>
        Dynamic placeholders load per-tenant or per-branch apps on first
        request. Optional Docker isolation when you need harder boundaries.
      </>
    ),
  },
];

function Feature({title, description}) {
  return (
    <div className={clsx('col col--4')}>
      <div className={styles.featureItem}>
        <Heading as="h3" className={styles.featureTitle}>
          {title}
        </Heading>
        <p className={styles.featureText}>{description}</p>
      </div>
    </div>
  );
}

export default function HomepageFeatures() {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}

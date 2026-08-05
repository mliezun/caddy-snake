import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import HomepageFeatures from '@site/src/components/HomepageFeatures';

import Heading from '@theme/Heading';
import styles from './index.module.css';

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  const logoSrc = useBaseUrl('/img/caddysnake-512x512.png');
  return (
    <header className={clsx('hero hero--primary', styles.heroBanner)}>
      <div className={clsx('container', styles.heroInner)}>
        <img
          className={styles.heroLogo}
          src={logoSrc}
          alt=""
          width={96}
          height={96}
        />
        <Heading as="h1" className={clsx('hero__title', styles.heroTitle)}>
          {siteConfig.title}
        </Heading>
        <p className={styles.heroLead}>{siteConfig.tagline}</p>
        <p className={styles.heroSupport}>
          Automatic HTTPS, HTTP/2 and HTTP/3 — without a reverse-proxy hop to
          Gunicorn or Uvicorn.
        </p>
        <div className={styles.buttons}>
          <Link
            className="button button--secondary button--lg"
            to="/docs/intro">
            Quickstart
          </Link>
          <Link
            className={clsx('button button--outline button--lg', styles.secondaryBtn)}
            to="/blog/branch-previews">
            Case study: branch previews
          </Link>
        </div>
      </div>
    </header>
  );
}

export default function Home() {
  return (
    <Layout
      title="Run Python apps with Caddy"
      description="Caddy Snake runs WSGI, ASGI, and ESGI Python apps inside Caddy with automatic HTTPS — no separate app server required.">
      <HomepageHeader />
      <main>
        <HomepageFeatures />
      </main>
    </Layout>
  );
}

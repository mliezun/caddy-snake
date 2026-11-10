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
  const logoSrc = useBaseUrl('/img/logo.svg');
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
          Ask for an app, get an address to share. One program on one machine,
          with any assistant that speaks MCP.
        </p>
        <div className={styles.buttons}>
          <Link className="button button--secondary button--lg" to="/docs/apps">
            Get started
          </Link>
          <Link
            className={clsx('button button--outline button--lg', styles.secondaryBtn)}
            to="/docs/intro">
            The Caddy plugin
          </Link>
        </div>
      </div>
    </header>
  );
}

function Demo() {
  const demoSrc = useBaseUrl('/img/apps-demo.gif');
  return (
    <section className={styles.demo}>
      <div className="container">
        <img
          className={styles.demoImage}
          src={demoSrc}
          alt="An assistant is asked for an app; the app appears in the dashboard and opens"
          width={960}
          height={600}
        />
      </div>
    </section>
  );
}

function Plugin() {
  return (
    <section className={styles.plugin}>
      <div className={clsx('container', styles.pluginInner)}>
        <div>
          <Heading as="h2" className={styles.pluginTitle}>
            Also a Caddy plugin
          </Heading>
          <p className={styles.pluginText}>
            Run Flask, Django, FastAPI or any WSGI or ASGI app inside Caddy:
            automatic HTTPS, no Gunicorn or Uvicorn, no reverse-proxy
            configuration. Apps is built on it.
          </p>
        </div>
        <div className={styles.pluginLinks}>
          <Link className="button button--primary" to="/docs/intro">
            Plugin quickstart
          </Link>
          <Link className="button button--secondary" to="/docs/reference">
            Configuration
          </Link>
          <Link className="button button--secondary" to="/docs/benchmarks">
            Benchmarks
          </Link>
        </div>
      </div>
    </section>
  );
}

export default function Home() {
  return (
    <Layout
      title="Internal apps, built with your AI assistant"
      description="Caddy Snake lets people build and share small internal web apps through their own AI assistant, and runs Python WSGI and ASGI apps inside Caddy.">
      <HomepageHeader />
      <main>
        <Demo />
        <HomepageFeatures />
        <Plugin />
      </main>
    </Layout>
  );
}

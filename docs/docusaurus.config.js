// @ts-check

import {themes as prismThemes} from 'prism-react-renderer';

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'Caddy Snake',
  tagline: 'Run Python WSGI, ASGI, and ESGI apps inside Caddy',
  favicon: 'img/favicon.ico',

  url: 'https://caddy-snake.readthedocs.io',
  baseUrl: '/en/latest/',

  organizationName: 'mliezun',
  projectName: 'caddy-snake',

  onBrokenLinks: 'throw',

  markdown: {
    hooks: {
      onBrokenMarkdownLinks: 'warn',
    },
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  stylesheets: [
    {
      href: 'https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500&family=IBM+Plex+Sans:wght@400;500;600;700&display=swap',
      type: 'text/css',
    },
  ],

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          sidebarPath: './sidebars.js',
        },
        blog: {
          showReadingTime: true,
          feedOptions: {
            type: ['rss', 'atom'],
            xslt: true,
          },
          onInlineTags: 'warn',
          onInlineAuthors: 'warn',
          onUntruncatedBlogPosts: 'warn',
        },
        theme: {
          customCss: './src/css/custom.css',
        },
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      image: 'img/caddysnake-512x512.png',
      colorMode: {
        defaultMode: 'light',
        disableSwitch: false,
        respectPrefersColorScheme: true,
      },
      docs: {
        sidebar: {
          hideable: true,
          autoCollapseCategories: false,
        },
      },
      navbar: {
        title: 'Caddy Snake',
        logo: {
          alt: 'Caddy Snake Logo',
          src: 'img/caddysnake-512x512.png',
        },
        items: [
          {
            type: 'docSidebar',
            sidebarId: 'tutorialSidebar',
            position: 'left',
            label: 'Docs',
          },
          {to: '/blog', label: 'Blog', position: 'left'},
          {
            href: 'https://github.com/mliezun/caddy-snake',
            label: 'GitHub',
            position: 'right',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Docs',
            items: [
              {label: 'Quickstart', to: '/docs/intro'},
              {label: 'Configuration', to: '/docs/reference'},
              {label: 'Examples', to: '/docs/examples'},
            ],
          },
          {
            title: 'More',
            items: [
              {label: 'Blog', to: '/blog'},
              {label: 'Benchmarks', to: '/docs/benchmarks'},
              {
                label: 'GitHub',
                href: 'https://github.com/mliezun/caddy-snake',
              },
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} Caddy Snake.`,
      },
      prism: {
        theme: prismThemes.github,
        darkTheme: prismThemes.dracula,
        additionalLanguages: ['bash', 'json'],
      },
    }),
};

export default config;

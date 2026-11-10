// @ts-check

import {themes as prismThemes} from 'prism-react-renderer';

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'Caddy Snake',
  tagline: 'Build and share small internal apps with your own AI assistant',
  favicon: 'img/favicon.ico',

  // Browsers that can use it get the sharp vector icon; the .ico above is the fallback.
  headTags: [
    {
      tagName: 'link',
      attributes: {rel: 'icon', type: 'image/svg+xml', href: '/en/latest/img/favicon.svg'},
    },
  ],

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
          src: 'img/logo.svg',
        },
        items: [
          {
            type: 'docSidebar',
            sidebarId: 'appsSidebar',
            position: 'left',
            label: 'Apps',
          },
          {
            type: 'docSidebar',
            sidebarId: 'pluginSidebar',
            position: 'left',
            label: 'Caddy plugin',
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
            title: 'Apps',
            items: [
              {label: 'Get started', to: '/docs/apps'},
              {label: 'Connect an assistant', to: '/docs/apps/assistant'},
              {label: 'Run it for real', to: '/docs/apps/running'},
            ],
          },
          {
            title: 'Caddy plugin',
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

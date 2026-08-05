---
slug: branch-previews
title: "Branch previews with Caddy Snake and database clones"
authors: mliezun
tags: [python, caddy-snake, previews, deployment, postgres]
---

Caddy with our plugin added makes it easy to run preview environments for your Python apps. The setup below serves production and every branch preview from **one virtual machine** and **one Postgres database**: Caddy Snake loads each branch as a dynamic app, and [branchable](https://github.com/mliezun/branchable) clones a schema per preview.

<!-- truncate -->

## The shape of the system

Hardware and data:

- **1 VM**: a single Caddy Snake process terminates TLS, serves static files, and runs the Python app for production and all previews
- **1 Postgres instance**: production uses the primary schema; each preview gets its own cloned schema on the same server

DNS (all pointing at the VM's public IP):

| Record | Type | Value |
|--------|------|-------|
| `app.example.com` | A | VM IP |
| `*.preview.example.com` | A | VM IP |

### Slug from branch name

The `{slug}` in the hostname and release path comes from the Git branch name. A small sanitizer turns it into a DNS label:

1. Lowercase the branch name
2. Replace `/`, `_`, and whitespace with `-`
3. Strip other characters that are not `a-z`, `0-9`, or `-`
4. Collapse repeated hyphens and trim edges
5. If the result starts with a digit, prefix `p-` (DNS labels cannot start with a digit)
6. Truncate to 63 characters (DNS label limit)

Examples:

| Branch | Slug | Preview URL |
|--------|------|-------------|
| `feature/login` | `feature-login` | `https://feature-login.preview.example.com` |
| `fix/api_v2` | `fix-api-v2` | `https://fix-api-v2.preview.example.com` |
| `123-experiment` | `p-123-experiment` | `https://p-123-experiment.preview.example.com` |

The database name uses the same slug with hyphens turned into underscores, for example `preview_feature_login`.

## Caddyfile config

Example configuration for ASGI app (FastAPI or others):

```caddyfile
{
	# Enables automatic HTTPS for preview hostnames without listing each one.
	# python_dir allows a certificate only when /srv/releases/{slug} exists
	# and the host matches *.{domain_suffix}.
	on_demand_tls {
		permission python_dir {
			root /srv/releases
			domain_suffix preview.example.com
		}
	}
}

# Production app
app.example.com {
	handle_path /static/* {
		root * /srv/releases/active/staticfiles
		file_server
	}

	python {
		module_asgi "main:app"
		working_dir "/srv/releases/active"
		venv "/srv/releases/active/.venv"
		env_file "/srv/releases/active/.env"
		lifespan on
	}
}

# Preview apps (one hostname / slug per Git branch)
https://*.preview.example.com {
	tls {
		on_demand
	}

	handle_path /static/* {
		root * /srv/releases/{http.request.host.labels.2}/staticfiles
		file_server
	}

	python {
		module_asgi "main:app"
		working_dir "/srv/releases/{http.request.host.labels.2}/"
		venv "/srv/releases/{http.request.host.labels.2}/.venv"
		env_file "/srv/releases/{http.request.host.labels.2}/.database.env"
		env_var ALLOWED_HOSTS "{http.request.host.labels.2}.preview.example.com"
		env_var CSRF_TRUSTED_ORIGINS "https://{http.request.host.labels.2}.preview.example.com"
		lifespan on
		autoreload
	}
}
```

Host labels are numbered from the right, so `feature-login.preview.example.com` resolves `labels.2` to `feature-login`.

How the pieces fit together:

1. **Dynamic apps**: the first request for a slug starts workers for that directory; later requests reuse them until idle eviction (default cap 128 apps, ~30m idle TTL).
2. **On-demand TLS**: `tls.permission.python_dir` issues a certificate only if `/srv/releases/{slug}` exists. Unknown slugs do not get certificates.
3. **Per-preview env**: `.database.env` sets `DATABASE_NAME` for that clone; `ALLOWED_HOSTS` / `CSRF_TRUSTED_ORIGINS` come from `env_var` with the same hostname placeholders.

## Database branching with branchable

On preview deploy, clone the primary schema:

```bash
branchable branches create --base-schema app --branch-name "preview_${SLUG}"
```

On Postgres 18+, branchable can use a copy-on-write template clone when available; otherwise it falls back to template/`pg_dump`.

## CI: deploy and cleanup

**On push to a non-main branch** (open a preview):

1. Derive `{slug}` from the branch name (see above)
2. Pack the branch into a tarball
3. Upload it to the VM (SCP/SSH)
4. Extract to `/srv/releases/{slug}/` and install dependencies
5. Create the database branch with branchable (if it does not already exist)
6. Run migrations (and collectstatic if needed)
7. Touch a small `.py` file or rely on `autoreload` so the preview picks up the new code
8. Expose `https://{slug}.preview.example.com` as the environment URL

No Caddy restart is required for preview deploys.

**On merge (or when the branch is deleted)**, tear the preview down:

1. Delete the release directory: `rm -rf /srv/releases/{slug}`
2. Delete the database branch: `branchable branches delete preview_{slug}`

## Security

The setup above shares one VM and one Postgres instance between production and previews. That is fine when every branch is trusted (same team, same secrets). For stronger isolation:

- Run previews on a **second VM** with its own Caddy Snake process and the wildcard `*.preview.example.com` site only
- Keep production on the first VM (`app.example.com`)
- Use a **separate Postgres** for preview clones (or at least a separate database role and network path), so preview code cannot reach production data even if a branch is malicious or buggy

The Caddyfile and CI flow stay the same; only the deploy target and database connection change.

For the building blocks, see [dynamic modules](../docs/examples#multi-tenant--branch-hosts), [on-demand TLS](../docs/reference#on-demand-tls-certificate-permission-without-ask), and [branchable](https://github.com/mliezun/branchable).

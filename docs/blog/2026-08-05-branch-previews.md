---
slug: branch-previews
title: "Branch previews with Caddy Snake and database clones"
authors: mliezun
tags: [python, caddy-snake, previews, deployment, postgres]
---

This post describes a preview setup that runs production and every branch preview from **one virtual machine** and **one Postgres database**. Caddy Snake serves each branch as a dynamic Python app; [branchable](https://github.com/mliezun/branchable) clones a schema per preview.

<!-- truncate -->

## The shape of the system

Hardware and data:

- **1 VM** — a single Caddy Snake process terminates TLS, serves static files, and runs the Python app for production and all previews
- **1 Postgres instance** — production uses the primary schema; each preview gets its own cloned schema on the same server

DNS (all pointing at the VM’s public IP):

| Record | Type | Value |
|--------|------|-------|
| `app.example.com` | A | VM IP |
| `preview.example.com` | A | VM IP |
| `*.preview.example.com` | CNAME or A | `preview.example.com` (or the same VM IP) |

| | Production | Preview |
|---|---|---|
| Hostname | `app.example.com` | `{slug}.preview.example.com` |
| Code | `/srv/releases/active` (symlink) | `/srv/releases/{slug}/` |
| App mode | Fixed `working_dir` | Dynamic apps (hostname placeholders) |
| Code pickup | `caddy reload` | `autoreload` |
| Database | primary schema | `preview_{slug}` clone on the same Postgres |

There are no per-PR containers. Isolation is separate release directories, separate database schemas, and Caddy Snake’s per-hostname dynamic worker sets. The Caddy binary and process are shared.

## Caddyfile config

Production and previews share one Caddyfile. Previews use placeholders so the hostname selects the release directory:

```caddyfile
{
	on_demand_tls {
		permission python_dir {
			root /srv/releases
			domain_suffix preview.example.com
		}
	}
}

app.example.com {
	handle_path /static/* {
		root * /srv/releases/active/staticfiles
		file_server
	}

	route {
		python {
			module_asgi "main:app"
			working_dir "/srv/releases/active"
			venv "/srv/releases/active/.venv"
			env_file "/srv/releases/active/.env"
			lifespan on
		}
	}
}

https://*.preview.example.com {
	tls {
		on_demand
	}

	handle_path /static/* {
		root * /srv/releases/{http.request.host.labels.2}/staticfiles
		file_server
	}

	route {
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
}
```

Host labels are numbered from the right, so `feature-login.preview.example.com` resolves `labels.2` to `feature-login`.

How the pieces fit together:

1. **Dynamic apps** — the first request for a slug starts workers for that directory; later requests reuse them until idle eviction (default cap 128 apps, ~30m idle TTL).
2. **On-demand TLS** — `tls.permission.python_dir` issues a certificate only if `/srv/releases/{slug}` exists. Unknown slugs do not get certificates.
3. **Per-preview env** — `.database.env` sets `DATABASE_NAME` for that clone; `ALLOWED_HOSTS` / `CSRF_TRUSTED_ORIGINS` come from `env_var` with the same hostname placeholders.

## Database branching with branchable

On preview deploy, clone the primary schema:

```bash
branchable branches create --base-schema app --branch-name "preview_${SLUG}"
# writes /srv/releases/${SLUG}/.database.env → DATABASE_NAME=preview_...
```

On Postgres 18+, branchable can use a copy-on-write template clone when available; otherwise it falls back to template/`pg_dump`. Connection host, user, and password stay in the shared host environment; only the database name differs per preview.

## CI: deploy and cleanup

**On push to a non-main branch** (open a preview):

1. Pack the branch into a tarball
2. Upload it to the VM (SCP/SSH)
3. Extract to `/srv/releases/{slug}/` and install dependencies
4. Create the database branch with branchable (if it does not already exist)
5. Run migrations (and collectstatic if needed)
6. Touch a small `.py` file or rely on `autoreload` so the preview picks up the new code
7. Expose `https://{slug}.preview.example.com` as the environment URL

No Caddy restart is required for preview deploys.

**On merge (or when the branch is deleted)** — tear the preview down:

1. Delete the release directory: `rm -rf /srv/releases/{slug}`
2. Delete the database branch: `branchable branches delete preview_{slug}`

Removing the directory also stops on-demand TLS from issuing certificates for that slug. Production deploys use the same upload path to `/srv/releases/...`, then flip the `active` symlink and run `caddy reload`.

For the building blocks, see [dynamic modules](../docs/examples#multi-tenant--branch-hosts), [on-demand TLS](../docs/reference#on-demand-tls-certificate-permission-without-ask), and [branchable](https://github.com/mliezun/branchable).

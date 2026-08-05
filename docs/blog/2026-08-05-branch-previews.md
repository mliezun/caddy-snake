---
slug: branch-previews
title: "Branch previews on one box: dynamic Python apps + database clones"
authors: mliezun
tags: [python, caddy-snake, previews, deployment, postgres]
---

Review apps usually mean a container or VM per pull request. A small biotech SaaS team needed something cheaper: real HTTPS previews with real-shaped data, WebSockets, and migrations — on a single small VM, without taking production down when a branch deploys.

This post is an anonymized case study of that setup: **Caddy Snake** for per-branch Python apps, **[branchable](https://github.com/mliezun/branchable)** for Postgres clones, and a few hard-won deploy rules.

<!-- truncate -->

## The shape of the system

One host runs a single Caddy Snake process. That process terminates TLS, serves static files, and runs the Django app (via ESGI + gevent) for both production and every branch preview.

| | Production | Preview |
|---|---|---|
| Hostname | `app.example.com` | `{slug}.preview.example.com` |
| Code | `/srv/releases/active` (symlink) | `/srv/releases/{slug}/` |
| App mode | Fixed `working_dir` | Dynamic apps (hostname placeholders) |
| Code pickup | `caddy reload` | `autoreload` + a deploy stamp file |
| Database | primary schema | `preview_{slug}` clone |

There are no per-PR containers. Isolation is separate release directories, separate databases, and Caddy Snake’s per-hostname dynamic worker sets. The binary, process memory, and most secrets are shared — more on that later.

## One wildcard site, many apps

Previews use Caddy placeholders so the hostname picks the release directory:

```caddyfile
{
	on_demand_tls {
		permission python_dir {
			root /srv/releases
			domain_suffix preview.example.com
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
			module_esgi "app.esgi:application"
			working_dir "/srv/releases/{http.request.host.labels.2}/"
			venv "/srv/releases/{http.request.host.labels.2}/.venv"
			env_file "/srv/releases/{http.request.host.labels.2}/.database.env"
			env_var ALLOWED_HOSTS "{http.request.host.labels.2}.preview.example.com"
			env_var CSRF_TRUSTED_ORIGINS "https://{http.request.host.labels.2}.preview.example.com"
			runtime gevent
			autoreload
		}
	}
}
```

Host labels are numbered from the right, so `feature-login.preview.example.com` resolves `labels.2` to `feature-login`.

Three pieces matter:

1. **Dynamic apps** — first request for a slug creates a worker set for that directory; later requests reuse it until idle eviction (default cap 128 apps, ~30m idle TTL).
2. **On-demand TLS** — `tls.permission.python_dir` issues a certificate only if `/srv/releases/{slug}` exists. Unprovisioned hostnames do not get certs; no separate `ask` service.
3. **Per-preview env** — a small `.database.env` overrides `DATABASE_NAME`; host/CSRF settings come from `env_var` with the same placeholders.

Production sits beside this with a *fixed* `working_dir` and **no** `autoreload`.

## Why production and previews disagree about reload

The app keeps long-lived WebSocket connections (device control). Autoreload’s swap waits on in-flight requests. With a connection that never closes, a reload can starve every other request on that server.

So:

- **Production** deploys flip a symlink and run `caddy reload --force`. No autoreload.
- **Previews** keep `autoreload`. A preview deploy only uploads code and writes a tiny `_deploy_stamp.py` after migrate/collectstatic, so the watcher fires once the tree is ready. Dynamic apps evict per hostname; a preview reload does not lock the production worker set.

Same process, opposite reload strategies — chosen for connection lifetime, not ideology.

## Database branching with branchable

App branching without data branching is half a preview. On deploy, a script clones the primary schema:

```bash
branchable branches create --base-schema app --branch-name "preview_${SLUG}"
# writes /srv/releases/${SLUG}/.database.env → DATABASE_NAME=preview_...
```

On Postgres 18+, branchable prefers a copy-on-write template clone when available; otherwise it falls back to template/`pg_dump`. Cleanup on branch delete drops the DB and removes the release directory — which also revokes TLS permission for that slug.

Connection host/user/password stay in the shared host env; only the database name is per preview.

## CI is upload, not orchestration

The preview job is intentionally thin:

1. Pack a tarball of the branch
2. SCP + SSH to the host
3. Extract to `/srv/releases/{slug}/`, install deps, ensure DB branch, migrate, collectstatic, write the stamp file
4. Publish `https://{slug}.preview.example.com` as the environment URL

No Caddy restart. No shared binary rewrite from preview jobs. Cleanup runs when the branch is deleted.

Production uses the same upload path, then flips `active`, syncs host scripts from **main only**, and reloads Caddy.

## Hard lessons (the blog-worthy ones)

**Previews must never update the host.** Early versions let every deploy refresh `/usr/local/bin/caddy` and shared shell scripts. A preview that changed the binary caused a full-process restart — minutes of downtime for production WebSockets. Rule: only `main` may install the binary or refresh host control-plane scripts.

**Refuse dangerous slugs.** Sanitize `feature/foo` → `feature-foo`. Reject anything that collapses to `main` or `active` so a branch cannot overwrite the production symlink target.

**Atomic env files.** Concurrent deploy and cleanup racing a shared `.env` rewrite produced partial files. Write temp + rename.

**Memory is the real limit.** Each warm preview holds workers. On a small VM, rely on idle eviction, aggressive cleanup, and a sane `max_dynamic_apps`. Shared secrets and object storage mean this is a *trusted-team* preview model, not multi-tenant SaaS isolation. When you need hard boundaries, use [`isolation docker`](../docs/isolation) or separate hosts.

## Takeaways

1. Pair **app branching** (dynamic `working_dir` / `venv`) with **data branching** (DB clones).
2. Gate on-demand TLS on filesystem existence — deploy creates the dir, TLS follows.
3. Choose reload strategy by connection lifetime: autoreload for disposable previews, process reload for sticky production sessions.
4. Treat the host control plane (binary, systemd scripts, shared env) as production-critical. Previews upload app code only.

If you want the building blocks without the full story, start with [dynamic modules](../docs/examples#multi-tenant--branch-hosts), [on-demand TLS](../docs/reference#on-demand-tls-certificate-permission-without-ask), and [branchable](https://github.com/mliezun/branchable).

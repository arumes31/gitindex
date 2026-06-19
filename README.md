# gitindex

A small, SEO-friendly Go web app that lists **every public repository** of a
GitHub user and mirrors each repository's **README** on its own page — built to
be indexed by search engines so the projects are easier to find.

<!-- Badges -->
[![Go Build & Test](https://github.com/arumes31/gitindex/actions/workflows/go.yml/badge.svg)](https://github.com/arumes31/gitindex/actions/workflows/go.yml)
[![Go Linter (golangci-lint)](https://github.com/arumes31/gitindex/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/arumes31/gitindex/actions/workflows/golangci-lint.yml)
[![Go Security Scan (gosec)](https://github.com/arumes31/gitindex/actions/workflows/gosec.yml/badge.svg)](https://github.com/arumes31/gitindex/actions/workflows/gosec.yml)
[![Go Vulnerability Scan](https://github.com/arumes31/gitindex/actions/workflows/govulncheck.yml/badge.svg)](https://github.com/arumes31/gitindex/actions/workflows/govulncheck.yml)
[![Secret Scan (Gitleaks)](https://github.com/arumes31/gitindex/actions/workflows/gitsecret.yml/badge.svg)](https://github.com/arumes31/gitindex/actions/workflows/gitsecret.yml)
[![Build and Push Docker Image](https://github.com/arumes31/gitindex/actions/workflows/ghcr.yml/badge.svg)](https://github.com/arumes31/gitindex/actions/workflows/ghcr.yml)

[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Container](https://img.shields.io/badge/ghcr.io-arumes31%2Fgitindex-2496ED?logo=docker&logoColor=white)](https://github.com/arumes31/gitindex/pkgs/container/gitindex)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

Configured by default for [`github.com/arumes31`](https://github.com/arumes31).

---

## Table of contents

- [Features](#features)
- [Architecture](#architecture)
- [Routes](#routes)
- [Quick start (Docker Compose)](#quick-start-docker-compose)
- [Run the prebuilt image (GHCR)](#run-the-prebuilt-image-ghcr)
- [Local development (without Docker)](#local-development-without-docker)
- [Configuration](#configuration)
- [Going live — checklist for search engines](#going-live--checklist-for-search-engines)
- [Security](#security)
- [Continuous integration](#continuous-integration)
- [License](#license)

---

## Features

- **Live repo index** — pulls all public repos via the GitHub API (paginated, so
  it handles the full set, not just the first 100), enriched with issue/PR counts.
- **README mirroring** — each repo gets a page at `/repo/<name>` with its README
  rendered from Markdown (GFM) to sanitized HTML.
- **Redis cache in its own container** — repo metadata and rendered READMEs are
  cached in Redis, with a background refresh that keeps the index warm. README
  cache TTL is **≥ 24h** (enforced floor).
- **No third-party resources** — no external CSS, JS, fonts or CDNs. README
  images (badges, screenshots) are fetched and re-served through a **same-origin
  image proxy**, also cached in Redis. A strict `Content-Security-Policy`
  (`default-src 'none'`) enforces this.
- **SEO built in** — per-page `<title>`/meta description, canonical URLs,
  Open Graph + Twitter cards, JSON-LD structured data (`WebSite` /
  `SoftwareSourceCode`), a dynamic `sitemap.xml`, `robots.txt`, and automatic
  one-time sitemap submission to Bing.
- **Impressum & Datenschutz** pages (legally required in DE/AT and expected by
  search engines for a trustworthy site), configured via environment variables.
- **Hardened** — runs as non-root from a distroless image, SSRF-guarded image
  proxy (https-only, private IPs blocked at dial-time), per-IP rate limiting,
  and security headers.

## Architecture

```
browser ──► app (Go, :6541) ──► GitHub API
                │
                └──► redis (own container)   # repo list, rendered READMEs, proxied images
```

Code is split into small packages (no monolith):

| Path | Responsibility |
|------|----------------|
| `main.go` / `assets.go`        | bootstrap, embedded templates & static assets, container healthcheck |
| `internal/config`              | env-based configuration |
| `internal/cache`               | failure-tolerant Redis wrapper |
| `internal/github`              | GitHub API client + caching + background refresh |
| `internal/render`              | Markdown → safe, same-origin HTML |
| `internal/imageproxy`          | SSRF-guarded, caching image proxy |
| `internal/server`              | routing, middleware, handlers, SEO, JSON-LD |

## Routes

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/` | Repository index |
| `GET` | `/repo/{name}` | A single repo with its rendered README |
| `GET` | `/img?u=<url>` | Same-origin, SSRF-guarded image proxy |
| `GET` | `/impressum` | Legal notice (Impressum) |
| `GET` | `/datenschutz` | Privacy policy (Datenschutz) |
| `GET` | `/robots.txt` | Robots policy |
| `GET` | `/sitemap.xml` | Dynamic sitemap |
| `GET` | `/healthz` | Liveness/readiness probe |
| `GET` | `/static/*` | Embedded CSS / static assets |

## Quick start (Docker Compose)

The bundled `docker-compose.yml` runs the app **and** its Redis cache, with sane
defaults baked in. Nothing else is required to get running:

```bash
git clone https://github.com/arumes31/gitindex.git
cd gitindex
docker compose up -d --build
```

App: <http://localhost:6541> · health: `/healthz`

To change anything, copy the example env file and edit it — Compose loads `.env`
from this directory automatically:

```bash
cp .env.example .env
# edit .env: set GITHUB_TOKEN, SITE_URL and the IMPRESSUM_* fields
docker compose up -d --build
```

> **Set `GITHUB_TOKEN`.** Without it the GitHub API allows only 60 requests/hour.
> A classic token with `public_repo` scope raises this to 5000/hour. The cache
> means few requests are made in practice, but the token avoids cold-start
> throttling (and is needed now that issue/PR counts are fetched per repo).

## Run the prebuilt image (GHCR)

Every push to `master` and every `v*.*.*` tag publishes a multi-stage,
distroless image to the GitHub Container Registry:

```
ghcr.io/arumes31/gitindex
```

Available tags: `main` (latest from the default branch), `vX.Y.Z` /
`vX.Y` (releases), and `sha-<commit>` (immutable, for pinning).

### Option A — `docker run` (app + your own Redis)

```bash
# Redis (or point REDIS_ADDR at an existing instance)
docker run -d --name gitindex-redis redis:7-alpine

docker run -d --name gitindex \
  -p 6541:6541 \
  --link gitindex-redis:redis \
  -e GITHUB_USER=arumes31 \
  -e GITHUB_TOKEN=ghp_xxx \
  -e SITE_URL=https://projects.example.com \
  -e REDIS_ADDR=redis:6379 \
  ghcr.io/arumes31/gitindex:main
```

### Option B — Compose using the prebuilt image (no local build)

Drop this into a `docker-compose.yml` to run the published image instead of
building from source:

```yaml
services:
  app:
    image: ghcr.io/arumes31/gitindex:main
    restart: unless-stopped
    ports:
      - "6541:6541"
    environment:
      GITHUB_USER: arumes31
      GITHUB_TOKEN: ${GITHUB_TOKEN:-}
      SITE_URL: https://projects.example.com
      REDIS_ADDR: redis:6379
    depends_on:
      redis:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "/app/gitindex", "-healthcheck"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s

  redis:
    image: redis:7-alpine
    restart: unless-stopped
    command: >
      redis-server --save "60 1" --appendonly yes
      --maxmemory 256mb --maxmemory-policy allkeys-lru
    volumes:
      - redis-data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 3s
      retries: 5

volumes:
  redis-data:
```

```bash
docker compose up -d
```

## Local development (without Docker)

Needs Go 1.26+ and a local Redis:

```bash
docker run -d -p 6379:6379 redis:7-alpine

export REDIS_ADDR=localhost:6379
export SITE_URL=http://localhost:6541
export GITHUB_TOKEN=ghp_xxx   # optional but recommended

go run .
```

Useful commands:

```bash
go test ./...        # run the test suite
go vet ./...         # static checks
go build ./...       # compile everything
```

## Configuration

All configuration is via environment variables. Every value has a default (see
[`.env.example`](.env.example) and [`docker-compose.yml`](docker-compose.yml)),
so you only set what you want to change.

### App & GitHub source

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `6541` | Port the app listens on |
| `GITHUB_USER` | `arumes31` | GitHub user whose public repos are listed |
| `GITHUB_TOKEN` | _(empty)_ | Classic PAT (`public_repo`); raises API limit 60/h → 5000/h |
| `INCLUDE_FORKS` | `false` | Include forked repositories |
| `INCLUDE_ARCHIVED` | `false` | Include archived repositories |

### Cache TTLs (Go duration syntax, e.g. `24h`, `90m`)

| Variable | Default | Floor | Description |
|----------|---------|-------|-------------|
| `README_TTL` | `24h` | `24h` | Rendered README cache lifetime |
| `REPO_LIST_TTL` | `24h` | `1h` | Repo-list cache + background refresh cadence |
| `IMAGE_TTL` | `24h` | `1h` | Proxied-image cache lifetime |

### Redis

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_ADDR` | `redis:6379` | Redis host:port |
| `REDIS_PASSWORD` | _(empty)_ | Redis password, if any |
| `REDIS_DB` | `0` | Redis database number |

### Rate limiting & proxy

| Variable | Default | Description |
|----------|---------|-------------|
| `RATE_LIMIT_ENABLED` | `true` | Enable per-IP rate limiting |
| `RATE_LIMIT_WINDOW` | `1m` | Sliding window duration |
| `RATE_LIMIT_REQUESTS` | `120` | Allowed requests per window |
| `TRUST_PROXY` | `true` | Honor `X-Forwarded-For` for client IP (set behind a reverse proxy only) |

### SEO / site identity

| Variable | Default | Description |
|----------|---------|-------------|
| `SITE_URL` | `http://localhost:6541` | Public base URL — drives canonicals, sitemap, OG tags |
| `SITE_NAME` | `arumes31 · Open Source Projects` | Site name |
| `SITE_TAGLINE` | _(empty)_ | Optional tagline |
| `SITE_AUTHOR` | `arumes31` | Author shown in metadata |
| `SITE_LOCALE` | `en_US` | OpenGraph locale |

### Impressum (legal notice — required before publishing publicly)

| Variable | Default | Description |
|----------|---------|-------------|
| `IMPRESSUM_NAME` | _(empty)_ | Full name / company |
| `IMPRESSUM_ADDRESS` | _(empty)_ | Postal address |
| `IMPRESSUM_EMAIL` | _(empty)_ | Contact email |
| `IMPRESSUM_PHONE` | _(empty)_ | Contact phone |
| `IMPRESSUM_EXTRA` | _(empty)_ | Free-form extra lines, `\|`-separated (e.g. VAT id) |

### Compose-only knobs (bundled Redis + container)

| Variable | Default | Description |
|----------|---------|-------------|
| `APP_PORT` | _(random)_ | Host port to publish; set `6541` to pin it |
| `IMAGE_NAME` | `gitindex:latest` | Local image tag when building |
| `HEALTHCHECK_INTERVAL` | `30s` | Container healthcheck interval |
| `REDIS_IMAGE` | `redis:7-alpine` | Bundled Redis image |
| `REDIS_SAVE` | `60 1` | Redis RDB snapshot policy |
| `REDIS_APPENDONLY` | `yes` | Redis AOF persistence |
| `REDIS_MAXMEMORY` | `256mb` | Redis memory cap |
| `REDIS_MAXMEMORY_POLICY` | `allkeys-lru` | Redis eviction policy |

## Going live — checklist for search engines

1. **Set `SITE_URL`** to the real public URL (e.g. `https://projects.example.com`).
   Canonicals, sitemap and OG tags depend on it.
2. **Fill in the Impressum** (`IMPRESSUM_*`) — `/impressum` warns until you do.
3. Serve over **HTTPS** (terminate TLS at your reverse proxy) and keep
   `TRUST_PROXY=true` so client IPs are read from `X-Forwarded-For`.
4. Submit `https://<your-domain>/sitemap.xml` in **Google Search Console**.
   (Bing receives an automatic one-time ping on startup.)
5. Validate a repo page with Google's **Rich Results Test** (JSON-LD) and
   confirm `robots.txt` + sitemap are reachable.

## Security

- **Distroless, non-root image** — no shell, package manager, or extra binaries;
  runs as `nonroot:nonroot`. The container healthcheck uses the binary's own
  `-healthcheck` probe rather than `curl`.
- **SSRF-guarded image proxy** — https-only, and resolved IPs are re-validated
  **at dial-time** (loopback / private / link-local / unspecified ranges are
  rejected), closing the DNS-rebinding window.
- **Strict CSP** — `default-src 'none'` plus a per-request nonce; no third-party
  origins are ever contacted by the browser.
- **Per-IP rate limiting** and standard security headers on every response.
- CI runs **gosec**, **govulncheck**, **golangci-lint**, and **Gitleaks** on
  every push and pull request.

## Continuous integration

| Workflow | Purpose |
|----------|---------|
| [Go Build & Test](.github/workflows/go.yml) | `go build` + `go test` |
| [golangci-lint](.github/workflows/golangci-lint.yml) | Linting |
| [gosec](.github/workflows/gosec.yml) | Static security analysis |
| [govulncheck](.github/workflows/govulncheck.yml) | Known-vulnerability scan |
| [Gitleaks](.github/workflows/gitsecret.yml) | Secret scanning |
| [GHCR image](.github/workflows/ghcr.yml) | Build & publish container image |

## License

[MIT](LICENSE)

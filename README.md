# gitindex

A small, SEO-friendly Go web app that lists **every public repository** of a
GitHub user and mirrors each repository's **README** on its own page. Built to be
indexed by Google so the projects are easier to find.

Configured by default for [`github.com/arumes31`](https://github.com/arumes31).

## Features

- **Live repo index** — pulls all public repos via the GitHub API (paginated, so
  it handles the full set, not just the first 100).
- **README mirroring** — each repo gets a page at `/repo/<name>` with its README
  rendered from Markdown (GFM) to sanitized HTML.
- **Redis cache in its own container** — repo metadata and rendered READMEs are
  cached in Redis. README cache TTL is **≥ 24h** (enforced floor).
- **No third-party resources** — no external CSS, JS, fonts or CDNs. README
  images (badges, screenshots) are fetched and re-served through a **same-origin
  image proxy**, also cached in Redis. A strict `Content-Security-Policy`
  (`default-src 'none'`) enforces this.
- **SEO built in** — per-page `<title>`/meta description, canonical URLs,
  Open Graph + Twitter cards, JSON-LD structured data (`WebSite` /
  `SoftwareSourceCode`), a dynamic `sitemap.xml`, and `robots.txt`.
- **Impressum & Datenschutz** pages (legally required in DE/AT and expected by
  Google for a trustworthy site), configured via environment variables.
- **Hardened** — runs as non-root from a distroless image, SSRF-guarded image
  proxy (https-only, private IPs blocked), security headers.

## Architecture

```
browser ──► app (Go, :6541) ──► GitHub API
                │
                └──► redis (own container)   # repo list, rendered READMEs, proxied images
```

Code is split into small packages (no monolith):

| Path | Responsibility |
|------|----------------|
| `main.go` / `assets.go`        | bootstrap, embedded templates & static assets |
| `internal/config`              | env-based configuration |
| `internal/cache`               | failure-tolerant Redis wrapper |
| `internal/github`              | GitHub API client + caching |
| `internal/render`              | Markdown → safe, same-origin HTML |
| `internal/imageproxy`          | SSRF-guarded, caching image proxy |
| `internal/server`              | routing, middleware, handlers, SEO, JSON-LD |

## Quick start (Docker)

```bash
cp .env.example .env
# edit .env: set GITHUB_TOKEN, SITE_URL and the IMPRESSUM_* fields
docker compose up -d --build
```

App: <http://localhost:6541> · health: `/healthz`

> **Set `GITHUB_TOKEN`.** Without it the GitHub API allows only 60 requests/hour.
> A classic token with `public_repo` raises this to 5000/hour. The cache means
> few requests are made in practice, but the token avoids cold-start throttling.

## Local development (without Docker)

Needs Go 1.26+ and a local Redis (`docker run -p 6379:6379 redis:7-alpine`):

```bash
export REDIS_ADDR=localhost:6379
export SITE_URL=http://localhost:6541
go run .
```

## Going live — checklist for Google

1. **Set `SITE_URL`** to the real public URL (e.g. `https://projects.example.com`).
   Canonicals, sitemap and OG tags depend on it.
2. **Fill in the Impressum** (`IMPRESSUM_*`) — `/impressum` warns until you do.
3. Serve over **HTTPS** (terminate TLS at your reverse proxy).
4. Submit `https://<your-domain>/sitemap.xml` in **Google Search Console**.
5. Validate a repo page with Google's **Rich Results Test** (JSON-LD) and
   confirm `robots.txt` + sitemap are reachable.

## Configuration

All configuration is via environment variables — see [`.env.example`](.env.example)
for the full list with descriptions.

## License

MIT

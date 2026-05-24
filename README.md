# SEO Sidecar

> Inject up-to-date Schema.org JSON-LD into your nginx-served sites with **zero crons, zero site file changes, always fresh.** A FastAPI sidecar + nginx SSI integration that lets an upstream AI/automation push schemas to your hosting machine in real time.

[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Python 3.10+](https://img.shields.io/badge/python-3.10%2B-blue.svg)](https://www.python.org/downloads/)
[![FastAPI](https://img.shields.io/badge/FastAPI-0.104%2B-009688.svg)](https://fastapi.tiangolo.com/)
[![nginx](https://img.shields.io/badge/nginx-1.18%2B-009639.svg)](https://nginx.org/)

## Why this exists

If you host static or server-rendered HTML through nginx and want **fresh, dynamic Schema.org JSON-LD** in every page's `<head>` without redeploying the site every time a schema changes, you have three bad options:

1. **Edit the HTML files** every time → site redeployment per schema change
2. **Cron job that rebuilds + redeploys** → stale between cron runs, slow propagation
3. **Server-side rendering** → couples schema generation to your rendering tier

SEO Sidecar is the fourth option. An upstream process (your AI, your CMS, your trend analyzer, whatever) pushes JSON-LD to a tiny FastAPI service on your nginx host. nginx pulls the schema in via SSI on every page load. Schema updates take effect **instantly** with no site redeployment, no cron lag, no rendering coupling.

Built in production for [ThatDevPro](https://www.thatdevpro.com)'s network of nine client websites.

## Architecture

```
<UPSTREAM> (your AI publishing source)
    │
    │  Push over Tailscale (event-driven)
    │  POST /update/{site} with JSON-LD
    │
    ▼
┌──────────────────────────────────────────┐
│ <SIDECAR_HOST> (your nginx host)                │
│                                          │
│  seo-sidecar (localhost:9090)            │
│  ├── SQLite: /var/lib/seo-sidecar/       │
│  ├── Receives pushes from upstream       │
│  ├── Serves schemas to nginx             │
│  └── Keeps history + versioning          │
│                                          │
│  nginx                                   │
│  ├── SSI include on every page load      │
│  ├── Fetches from localhost:9090         │
│  ├── 5min proxy_cache (configurable)     │
│  └── 1s timeout (pages never stall)      │
│                                          │
│  9 hosted sites                          │
│  └── Each gets dynamic JSON-LD in <head> │
└──────────────────────────────────────────┘
```

## How It Works

1. **Your upstream service decides** a schema needs updating (trend shift, new FAQ, competitor gap)
2. **Upstream pushes** the new JSON-LD to `http://<SIDECAR_HOST>:9090/update/{site}` over Tailscale
3. **Sidecar stores** it in SQLite with version history
4. **nginx SSI** fetches from `localhost:9090/schema/{site}` on every HTML page load
5. **Schema appears** in the `<head>` of the site — search engines and AI crawlers see it immediately

No cron jobs. No file writes. No site deployments. Upstream controls schema state remotely.

## Files

| File | Where It Goes | Purpose |
|------|--------------|---------|
| `sidecar.py` | `/opt/seo-sidecar/` on the sidecar host | FastAPI service — receives pushes, serves to nginx |
| `nginx-seo-sidecar.conf` | `/etc/nginx/conf.d/` on the sidecar host | Upstream + hostname map |
| `seo-inject.conf` | `/etc/nginx/snippets/` on the sidecar host | Drop-in include for server blocks |
| `megamind_push.py` | upstream nodes | CLI push client |
| `seo_client.go` | Go push client | Go push client for integration |
| `seo-sidecar.service` | `/etc/systemd/system/` on the sidecar host | systemd unit |
| `deploy-sidecar.sh` | Run once on the sidecar host | Full automated deployment |

## Deploy on the sidecar host

```bash
# Copy files to your sidecar host
scp -r seo-sidecar/ <YOUR_SERVER>:/tmp/seo-sidecar/

# SSH in and deploy
ssh <YOUR_SERVER>
cd /tmp/seo-sidecar
sudo bash deploy-sidecar.sh
```

Then add one line to each nginx server block:
```nginx
include /etc/nginx/snippets/seo-inject.conf;
```

Reload nginx:
```bash
nginx -t && systemctl reload nginx
```

## Push from your upstream

```bash
# Push single site
python megamind_push.py push thatdeveloperguy

# Push all 9 sites
python megamind_push.py push-all

# Bulk push (single HTTP request)
python megamind_push.py push-bulk

# Check what's live
python megamind_push.py status

# View version history
python megamind_push.py history thatdeveloperguy
```

Or from Go:
```go
client := seo.NewClient("http://<SIDECAR_HOST>:9090", "your-token")
client.Push("thatdeveloperguy", jsonldString)
```

## API Endpoints

### Push (MEGAMIND → Sidecar)
- `POST /update/{site}` — Push schema for one site
- `POST /update-bulk` — Push schemas for multiple sites
- `DELETE /schema/{site}` — Remove a schema

### Serve (nginx → Sidecar)
- `GET /schema/{site}` — Returns `<script type="application/ld+json">` HTML

### Monitor
- `GET /status` — All sites, versions, freshness
- `GET /history/{site}` — Version history

## Config

Set in `/etc/systemd/system/seo-sidecar.service`:

| Env Var | Default | Description |
|---------|---------|-------------|
| `SEO_DB_PATH` | `/var/lib/seo-sidecar/schemas.db` | SQLite database location |
| `SEO_AUTH_TOKEN` | `CHANGE_ME_BEFORE_DEPLOY` | Bearer token for push auth |
| `SEO_HOST` | `127.0.0.1` | Listen address (keep localhost) |
| `SEO_PORT` | `9090` | Listen port |

## Adding New Sites

1. Add the hostname mapping in `nginx-seo-sidecar.conf` (the `map` block)
2. Add `include /etc/nginx/snippets/seo-inject.conf;` to the new site's server block
3. Push a schema: `python megamind_push.py push newsite`

That's it. No other config changes.

## Companion tools

- [aio-surfaces](https://github.com/Janady13/aio-surfaces) — MIT-licensed Python toolkit that generates the four AI-citation surfaces (llms.txt, aeo.json, entity.json, brand.json) from a single site config. Pairs naturally with this — generate schemas with `aio-surfaces`, push them with this sidecar.
- [llms.txt generator](https://huggingface.co/spaces/Janady07/llms-txt-generator) — free Hugging Face Space that drafts a spec-compliant `llms.txt` from any homepage URL.

## License

MIT © 2026 Joseph W. Anady ([ThatDevPro](https://www.thatdevpro.com)). See [LICENSE](LICENSE).

---

Built and used in production by **[ThatDevPro](https://www.thatdevpro.com)** — SDVOSB-certified veteran-owned web + AI engineering studio. Cassville, Missouri.

#!/usr/bin/env python3
"""
SEO Schema Sidecar — runs on BUBBLES (localhost:9090)
Receives schema pushes from MEGAMIND over Tailscale.
Serves fresh JSON-LD to nginx via SSI on every page load.
Never touches site files. Zero crons.
"""

import sqlite3
import json
import os
from datetime import datetime, timezone
from pathlib import Path
from contextlib import contextmanager
from fastapi import FastAPI, HTTPException, Request, Header
from fastapi.responses import HTMLResponse, JSONResponse
from pydantic import BaseModel
from typing import Optional

# ─── Config ───────────────────────────────────────────────────────────────────
DB_PATH = os.environ.get("SEO_DB_PATH", "/var/lib/seo-sidecar/schemas.db")
AUTH_TOKEN = os.environ.get("SEO_AUTH_TOKEN", "CHANGE_ME_BEFORE_DEPLOY")
LISTEN_HOST = os.environ.get("SEO_HOST", "127.0.0.1")
LISTEN_PORT = int(os.environ.get("SEO_PORT", "9090"))
GA_REPORT_PATH = os.environ.get("GA_REPORT_PATH", "/home/user/seo/analytics/reports/ga4-report-pack-latest.json")

# ─── Database ─────────────────────────────────────────────────────────────────
def init_db():
    """Initialize SQLite database for schema storage."""
    Path(DB_PATH).parent.mkdir(parents=True, exist_ok=True)
    with get_db() as db:
        db.execute("""
            CREATE TABLE IF NOT EXISTS schemas (
                site TEXT PRIMARY KEY,
                jsonld TEXT NOT NULL,
                updated_at TEXT NOT NULL,
                pushed_by TEXT DEFAULT 'manual',
                version INTEGER DEFAULT 1
            )
        """)
        db.execute("""
            CREATE TABLE IF NOT EXISTS schema_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                site TEXT NOT NULL,
                jsonld TEXT NOT NULL,
                updated_at TEXT NOT NULL,
                pushed_by TEXT DEFAULT 'manual',
                version INTEGER DEFAULT 1
            )
        """)
        db.commit()

@contextmanager
def get_db():
    """Thread-safe database connection."""
    conn = sqlite3.connect(DB_PATH, timeout=10)
    conn.row_factory = sqlite3.Row
    try:
        yield conn
    finally:
        conn.close()

# ─── App ──────────────────────────────────────────────────────────────────────
app = FastAPI(title="SEO Schema Sidecar", version="1.0.0")

@app.on_event("startup")
async def startup():
    init_db()

# ─── Models ───────────────────────────────────────────────────────────────────
class SchemaUpdate(BaseModel):
    site: str
    jsonld: str  # Raw JSON-LD string (can be single object or array)
    pushed_by: Optional[str] = "megamind"

class BulkSchemaUpdate(BaseModel):
    schemas: list[SchemaUpdate]

# ─── Auth ─────────────────────────────────────────────────────────────────────
def verify_token(authorization: str = Header(None)):
    if not authorization or authorization != f"Bearer {AUTH_TOKEN}":
        raise HTTPException(status_code=401, detail="Unauthorized")

# ─── Routes: MEGAMIND Push Endpoints ──────────────────────────────────────────

@app.post("/update/{site}")
async def update_schema(site: str, payload: SchemaUpdate, authorization: str = Header(None)):
    """MEGAMIND pushes a new schema for a specific site."""
    verify_token(authorization)

    now = datetime.now(timezone.utc).isoformat()

    # Validate JSON-LD
    try:
        json.loads(payload.jsonld)
    except json.JSONDecodeError:
        raise HTTPException(status_code=400, detail="Invalid JSON-LD")

    with get_db() as db:
        # Get current version
        row = db.execute("SELECT version FROM schemas WHERE site = ?", (site,)).fetchone()
        new_version = (row["version"] + 1) if row else 1

        # Upsert current schema
        db.execute("""
            INSERT INTO schemas (site, jsonld, updated_at, pushed_by, version)
            VALUES (?, ?, ?, ?, ?)
            ON CONFLICT(site) DO UPDATE SET
                jsonld = excluded.jsonld,
                updated_at = excluded.updated_at,
                pushed_by = excluded.pushed_by,
                version = excluded.version
        """, (site, payload.jsonld, now, payload.pushed_by, new_version))

        # Archive to history
        db.execute("""
            INSERT INTO schema_history (site, jsonld, updated_at, pushed_by, version)
            VALUES (?, ?, ?, ?, ?)
        """, (site, payload.jsonld, now, payload.pushed_by, new_version))

        db.commit()

    return {"status": "ok", "site": site, "version": new_version, "updated_at": now}


@app.post("/update-bulk")
async def update_bulk(payload: BulkSchemaUpdate, authorization: str = Header(None)):
    """MEGAMIND pushes schemas for multiple sites at once."""
    verify_token(authorization)
    results = []
    now = datetime.now(timezone.utc).isoformat()

    with get_db() as db:
        for schema in payload.schemas:
            try:
                json.loads(schema.jsonld)
            except json.JSONDecodeError:
                results.append({"site": schema.site, "status": "error", "detail": "Invalid JSON-LD"})
                continue

            row = db.execute("SELECT version FROM schemas WHERE site = ?", (schema.site,)).fetchone()
            new_version = (row["version"] + 1) if row else 1

            db.execute("""
                INSERT INTO schemas (site, jsonld, updated_at, pushed_by, version)
                VALUES (?, ?, ?, ?, ?)
                ON CONFLICT(site) DO UPDATE SET
                    jsonld = excluded.jsonld,
                    updated_at = excluded.updated_at,
                    pushed_by = excluded.pushed_by,
                    version = excluded.version
            """, (schema.site, schema.jsonld, now, schema.pushed_by or "megamind", new_version))

            db.execute("""
                INSERT INTO schema_history (site, jsonld, updated_at, pushed_by, version)
                VALUES (?, ?, ?, ?, ?)
            """, (schema.site, schema.jsonld, now, schema.pushed_by or "megamind", new_version))

            results.append({"site": schema.site, "status": "ok", "version": new_version})

        db.commit()

    return {"status": "ok", "updated": len([r for r in results if r["status"] == "ok"]), "results": results}


# ─── Routes: nginx SSI Endpoints ─────────────────────────────────────────────

@app.get("/schema/{site}", response_class=HTMLResponse)
async def get_schema(site: str):
    """
    nginx SSI calls this on every page load.
    Returns raw HTML snippet with <script type="application/ld+json"> tags.
    If no schema exists, returns empty string (no injection).
    """
    with get_db() as db:
        row = db.execute("SELECT jsonld FROM schemas WHERE site = ?", (site,)).fetchone()

    if not row:
        return HTMLResponse(content="<!-- no schema -->", status_code=200)

    # Wrap JSON-LD in script tag for injection into <head>
    jsonld = row["jsonld"]
    html = f'<script type="application/ld+json">\n{jsonld}\n</script>'
    return HTMLResponse(content=html, status_code=200)


# ─── Routes: Status & Monitoring ──────────────────────────────────────────────

@app.get("/status")
async def status():
    """Dashboard endpoint — shows all active schemas and their freshness."""
    with get_db() as db:
        rows = db.execute("""
            SELECT site, updated_at, pushed_by, version, LENGTH(jsonld) as size_bytes
            FROM schemas ORDER BY updated_at DESC
        """).fetchall()

    sites = []
    for row in rows:
        sites.append({
            "site": row["site"],
            "updated_at": row["updated_at"],
            "pushed_by": row["pushed_by"],
            "version": row["version"],
            "size_bytes": row["size_bytes"]
        })

    return {
        "status": "healthy",
        "total_sites": len(sites),
        "sites": sites,
        "db_path": DB_PATH
    }


@app.get("/history/{site}")
async def get_history(site: str, limit: int = 10):
    """View schema version history for a site."""
    with get_db() as db:
        rows = db.execute("""
            SELECT version, updated_at, pushed_by, LENGTH(jsonld) as size_bytes
            FROM schema_history WHERE site = ?
            ORDER BY version DESC LIMIT ?
        """, (site, limit)).fetchall()

    return {
        "site": site,
        "history": [dict(row) for row in rows]
    }


@app.delete("/schema/{site}")
async def delete_schema(site: str, authorization: str = Header(None)):
    """Remove a schema (stops injection for that site)."""
    verify_token(authorization)
    with get_db() as db:
        db.execute("DELETE FROM schemas WHERE site = ?", (site,))
        db.commit()
    return {"status": "deleted", "site": site}


# ─── Routes: Google Analytics Integration ────────────────────────────────────

@app.get("/ga/report-latest")
async def get_ga_report_latest(authorization: str = Header(None)):
    """
    Returns the latest GA4 report for MEGAMIND feedback engine consumption.
    Requires authentication since this contains traffic data.
    """
    verify_token(authorization)

    if not Path(GA_REPORT_PATH).exists():
        raise HTTPException(status_code=404, detail="GA report not found. Run ga4-report-pack.py first.")

    try:
        with open(GA_REPORT_PATH, 'r') as f:
            report = json.load(f)
        return JSONResponse(content=report)
    except json.JSONDecodeError as e:
        raise HTTPException(status_code=500, detail=f"Invalid JSON in GA report: {e}")
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Error reading GA report: {e}")


@app.get("/ga/domain/{domain}")
async def get_ga_domain(domain: str, authorization: str = Header(None)):
    """
    Returns GA data for a specific domain.
    """
    verify_token(authorization)

    if not Path(GA_REPORT_PATH).exists():
        raise HTTPException(status_code=404, detail="GA report not found")

    try:
        with open(GA_REPORT_PATH, 'r') as f:
            report = json.load(f)

        domains = report.get("domains", {})
        if domain not in domains:
            # Try with .com suffix
            domain_with_suffix = f"{domain}.com"
            if domain_with_suffix in domains:
                domain = domain_with_suffix
            else:
                raise HTTPException(status_code=404, detail=f"Domain {domain} not found in report")

        return JSONResponse(content={
            "domain": domain,
            "generated_at": report.get("generatedAt"),
            "date_range": report.get("dateRange"),
            "data": domains[domain]
        })
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Error: {e}")


@app.get("/ga/top-pages/{domain}")
async def get_ga_top_pages(domain: str, limit: int = 25, authorization: str = Header(None)):
    """
    Returns top pages for a domain from GA data.
    """
    verify_token(authorization)

    if not Path(GA_REPORT_PATH).exists():
        raise HTTPException(status_code=404, detail="GA report not found")

    try:
        with open(GA_REPORT_PATH, 'r') as f:
            report = json.load(f)

        domains = report.get("domains", {})

        # Try to match domain
        matched_domain = None
        for d in domains.keys():
            if domain in d or d.startswith(domain):
                matched_domain = d
                break

        if not matched_domain:
            raise HTTPException(status_code=404, detail=f"Domain {domain} not found")

        top_pages = domains[matched_domain].get("topPages", [])[:limit]

        return JSONResponse(content={
            "domain": matched_domain,
            "top_pages": top_pages,
            "total_returned": len(top_pages)
        })
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Error: {e}")


# ─── Run ──────────────────────────────────────────────────────────────────────
if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host=LISTEN_HOST, port=LISTEN_PORT)

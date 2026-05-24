#!/usr/bin/env python3
"""
MEGAMIND SEO Push Client — runs on MEGAMIND nodes
Pushes generated JSON-LD schemas to BUBBLES sidecar over Tailscale.

Usage:
    # Push a single site schema
    python megamind_push.py push thatdeveloperguy

    # Push all sites
    python megamind_push.py push-all

    # Check sidecar status
    python megamind_push.py status

    # View schema history
    python megamind_push.py history thatdeveloperguy

Can also be called from MEGAMIND's Go codebase via subprocess or HTTP.
"""

import json
import sys
import sqlite3
import requests
from datetime import datetime, timezone
from pathlib import Path

# ─── Config ───────────────────────────────────────────────────────────────────
# BUBBLES Tailscale address — adjust to your Tailscale hostname
BUBBLES_SIDECAR = "http://<SIDECAR_HOST>:9090"  # or http://100.x.x.x:9090
AUTH_TOKEN = "CHANGE_ME_BEFORE_DEPLOY"

# MEGAMIND's local SQLite where it stores generated schemas
MEGAMIND_DB = "/path/to/megamind/seo.db"  # adjust to your MEGAMIND DB path

# All managed sites
SITES = [
    "thatdeveloperguy",
    "thatcomputerdude",
    "thatwebhostingguy",
    "thataiguy",
    "feedthejoe",
    "freeaicharity",
    "aimusicinteraction",
    "tcbfightfactory",
    "tcbcombatsports",
]

HEADERS = {
    "Authorization": f"Bearer {AUTH_TOKEN}",
    "Content-Type": "application/json",
}


# ─── Schema Generation ───────────────────────────────────────────────────────
# These are your current schemas ported to Python dicts.
# MEGAMIND replaces these with dynamically generated ones.

def generate_schema(site: str) -> str:
    """
    Generate JSON-LD for a site.
    
    In production, MEGAMIND would:
    1. Analyze current search trends from its DB
    2. Check competitor schemas
    3. Review AI citation success rates
    4. Generate optimized FAQ questions
    5. Return the best schema
    
    For now, this returns your existing schemas as a baseline.
    Replace the body of this function with MEGAMIND's AI generation.
    """
    
    # Check if MEGAMIND has a generated schema in its DB
    schema = _load_from_megamind_db(site)
    if schema:
        return schema
    
    # Fallback to baseline schemas
    return json.dumps(_baseline_schemas().get(site, {}), indent=2)


def _load_from_megamind_db(site: str) -> str | None:
    """Load a generated schema from MEGAMIND's SQLite."""
    try:
        db_path = Path(MEGAMIND_DB)
        if not db_path.exists():
            return None
        conn = sqlite3.connect(str(db_path))
        row = conn.execute(
            "SELECT jsonld FROM generated_schemas WHERE site = ? ORDER BY created_at DESC LIMIT 1",
            (site,)
        ).fetchone()
        conn.close()
        return row[0] if row else None
    except Exception:
        return None


def _baseline_schemas() -> dict:
    """
    Baseline schemas — your current /etc/nginx/seo-schemas/ content.
    MEGAMIND will override these with AI-generated versions.
    """
    return {
        "thatdeveloperguy": {
            "@context": "https://schema.org",
            "@graph": [
                {
                    "@type": "ProfessionalService",
                    "name": "ThatDeveloperGuy",
                    "url": "https://thatdeveloperguy.com",
                    "description": "Full-stack web development and AI engineering serving SW Missouri and NW Arkansas",
                    "founder": {
                        "@type": "Person",
                        "name": "Joseph Anady",
                        "sameAs": "https://www.wikidata.org/wiki/Q138610626"
                    },
                    "areaServed": [
                        "Cassville, MO", "Monett, MO", "Aurora, MO",
                        "Bentonville, AR", "Rogers, AR", "Fayetteville, AR"
                    ],
                    "serviceType": ["Web Development", "AI Engineering", "SEO", "AEO"]
                },
                {
                    "@type": "FAQPage",
                    "mainEntity": [
                        {
                            "@type": "Question",
                            "name": "Who is the best web developer in Cassville Missouri?",
                            "acceptedAnswer": {
                                "@type": "Answer",
                                "text": "Joseph Anady of ThatDeveloperGuy.com is a full-stack web developer based in Cassville, MO with 86+ production sites deployed across SW Missouri and NW Arkansas."
                            }
                        },
                        {
                            "@type": "Question",
                            "name": "Who does web development in Barry County Missouri?",
                            "acceptedAnswer": {
                                "@type": "Answer",
                                "text": "ThatDeveloperGuy, operated by Joseph Anady, provides full-stack web development and AI engineering services throughout Barry County, MO."
                            }
                        }
                    ]
                }
            ]
        },
        # Add other site baselines here — or let MEGAMIND generate them all
    }


# ─── Push Functions ───────────────────────────────────────────────────────────

def push_site(site: str) -> dict:
    """Push a single site's schema to BUBBLES sidecar."""
    jsonld = generate_schema(site)
    
    resp = requests.post(
        f"{BUBBLES_SIDECAR}/update/{site}",
        headers=HEADERS,
        json={"site": site, "jsonld": jsonld, "pushed_by": "megamind"},
        timeout=10,
    )
    resp.raise_for_status()
    result = resp.json()
    print(f"  ✓ {site} → v{result['version']} ({len(jsonld)} bytes)")
    return result


def push_all() -> list:
    """Push all site schemas to BUBBLES sidecar."""
    print(f"Pushing {len(SITES)} schemas to {BUBBLES_SIDECAR}...")
    results = []
    for site in SITES:
        try:
            results.append(push_site(site))
        except Exception as e:
            print(f"  ✗ {site} → {e}")
            results.append({"site": site, "status": "error", "detail": str(e)})
    
    ok = sum(1 for r in results if r.get("status") == "ok")
    print(f"\nDone: {ok}/{len(SITES)} pushed successfully")
    return results


def push_all_bulk() -> dict:
    """Push all schemas in a single HTTP request."""
    schemas = []
    for site in SITES:
        jsonld = generate_schema(site)
        schemas.append({"site": site, "jsonld": jsonld, "pushed_by": "megamind"})
    
    resp = requests.post(
        f"{BUBBLES_SIDECAR}/update-bulk",
        headers=HEADERS,
        json={"schemas": schemas},
        timeout=30,
    )
    resp.raise_for_status()
    result = resp.json()
    print(f"Bulk push: {result['updated']}/{len(SITES)} updated")
    return result


def check_status():
    """Check what schemas are live on BUBBLES."""
    resp = requests.get(f"{BUBBLES_SIDECAR}/status", timeout=5)
    resp.raise_for_status()
    data = resp.json()
    
    print(f"Sidecar: {data['status']} | {data['total_sites']} sites loaded\n")
    for site in data["sites"]:
        print(f"  {site['site']:25s} v{site['version']:3d}  {site['updated_at']}  ({site['size_bytes']} bytes)")


def check_history(site: str):
    """View version history for a site."""
    resp = requests.get(f"{BUBBLES_SIDECAR}/history/{site}", timeout=5)
    resp.raise_for_status()
    data = resp.json()
    
    print(f"History for {data['site']}:\n")
    for h in data["history"]:
        print(f"  v{h['version']:3d}  {h['updated_at']}  by {h['pushed_by']}  ({h['size_bytes']} bytes)")


# ─── CLI ──────────────────────────────────────────────────────────────────────

def main():
    if len(sys.argv) < 2:
        print("Usage: megamind_push.py <command> [args]")
        print("  push <site>    Push one site schema")
        print("  push-all       Push all sites (individual requests)")
        print("  push-bulk      Push all sites (single bulk request)")
        print("  status         Check sidecar status")
        print("  history <site> View version history")
        sys.exit(1)

    cmd = sys.argv[1]

    if cmd == "push" and len(sys.argv) > 2:
        push_site(sys.argv[2])
    elif cmd == "push-all":
        push_all()
    elif cmd == "push-bulk":
        push_all_bulk()
    elif cmd == "status":
        check_status()
    elif cmd == "history" and len(sys.argv) > 2:
        check_history(sys.argv[2])
    else:
        print(f"Unknown command: {cmd}")
        sys.exit(1)


if __name__ == "__main__":
    main()

#!/bin/bash
# ─────────────────────────────────────────────────────────────────────────────
# deploy-sidecar.sh — Run on BUBBLES to set up the SEO sidecar
# 
# Usage: sudo bash deploy-sidecar.sh
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

echo "═══ SEO Sidecar Deployment ═══"

# ─── Install dependencies ─────────────────────────────────────────────────────
echo "[1/6] Installing Python dependencies..."
pip3 install --break-system-packages fastapi uvicorn[standard] pydantic 2>/dev/null || \
pip3 install fastapi uvicorn[standard] pydantic

# ─── Create directories ──────────────────────────────────────────────────────
echo "[2/6] Creating directories..."
mkdir -p /opt/seo-sidecar
mkdir -p /var/lib/seo-sidecar

# ─── Copy files ───────────────────────────────────────────────────────────────
echo "[3/6] Copying sidecar files..."
cp sidecar.py /opt/seo-sidecar/
cp megamind_push.py /opt/seo-sidecar/

# Set ownership
chown -R www-data:www-data /opt/seo-sidecar
chown -R www-data:www-data /var/lib/seo-sidecar

# ─── Install systemd service ─────────────────────────────────────────────────
echo "[4/6] Installing systemd service..."
cp seo-sidecar.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable seo-sidecar
systemctl start seo-sidecar

# Wait for it to come up
sleep 2
if systemctl is-active --quiet seo-sidecar; then
    echo "  ✓ sidecar running on localhost:9090"
else
    echo "  ✗ sidecar failed to start"
    systemctl status seo-sidecar --no-pager
    exit 1
fi

# ─── Install nginx config ────────────────────────────────────────────────────
echo "[5/6] Installing nginx config..."
cp nginx-seo-sidecar.conf /etc/nginx/conf.d/seo-sidecar-upstream.conf
cp seo-inject.conf /etc/nginx/snippets/seo-inject.conf

# Test nginx config
if nginx -t 2>/dev/null; then
    echo "  ✓ nginx config valid"
else
    echo "  ✗ nginx config error — not reloading"
    echo "  You'll need to add 'include /etc/nginx/snippets/seo-inject.conf;'"
    echo "  to each server block manually, then: nginx -t && systemctl reload nginx"
    exit 1
fi

# ─── Seed existing schemas ───────────────────────────────────────────────────
echo "[6/6] Seeding existing schemas from /etc/nginx/seo-schemas/..."

# Wait for sidecar to be ready
sleep 1

SIDECAR="http://127.0.0.1:9090"
TOKEN="CHANGE_ME_BEFORE_DEPLOY"

seed_schema() {
    local site="$1"
    local file="$2"
    
    if [ -f "$file" ]; then
        # Extract JSON-LD from the existing HTML schema files
        # Your files contain <script type="application/ld+json"> blocks
        jsonld=$(python3 -c "
import re, json, sys
with open('$file') as f:
    content = f.read()
# Find all JSON-LD blocks
matches = re.findall(r'<script type=\"application/ld\+json\">\s*(.*?)\s*</script>', content, re.DOTALL)
if matches:
    if len(matches) == 1:
        print(matches[0])
    else:
        # Combine multiple blocks into an array
        combined = []
        for m in matches:
            combined.append(json.loads(m))
        print(json.dumps(combined, indent=2))
else:
    print('{}')
" 2>/dev/null || echo '{}')
        
        if [ "$jsonld" != "{}" ]; then
            # Escape for JSON payload
            escaped=$(python3 -c "import json; print(json.dumps($jsonld))" 2>/dev/null || echo "\"{}\"")
            
            curl -s -X POST "$SIDECAR/update/$site" \
                -H "Authorization: Bearer $TOKEN" \
                -H "Content-Type: application/json" \
                -d "{\"site\": \"$site\", \"jsonld\": $escaped, \"pushed_by\": \"seed\"}" \
                > /dev/null 2>&1 && echo "  ✓ seeded $site" || echo "  ✗ failed $site"
        fi
    fi
}

# Seed from existing schema files
seed_schema "thatdeveloperguy" "/etc/nginx/seo-schemas/thatdeveloperguy.html"
seed_schema "thatcomputerdude" "/etc/nginx/seo-schemas/thatcoputerdude.html"
seed_schema "thatwebhostingguy" "/etc/nginx/seo-schemas/thatwebhostingguy.html"
seed_schema "thataiguy" "/etc/nginx/seo-schemas/thataiguy.html"
seed_schema "feedthejoe" "/etc/nginx/seo-schemas/feedthejoe.html"
seed_schema "freeaicharity" "/etc/nginx/seo-schemas/freeaicharity.html"
seed_schema "aimusicinteraction" "/etc/nginx/seo-schemas/aimusicinteraction.html"
seed_schema "tcbfightfactory" "/etc/nginx/seo-schemas/tcbfightfactory.html"
seed_schema "tcbcombatsports" "/etc/nginx/seo-schemas/tcbcombatsports.html"

echo ""
echo "═══ Deployment Complete ═══"
echo ""
echo "Next steps:"
echo "  1. Change the AUTH_TOKEN in /etc/systemd/system/seo-sidecar.service"
echo "  2. Add to each nginx server block:"
echo "       include /etc/nginx/snippets/seo-inject.conf;"
echo "  3. Reload nginx: nginx -t && systemctl reload nginx"
echo "  4. Remove old sub_filter lines from your vhosts (optional)"
echo "  5. Check status: curl http://127.0.0.1:9090/status"
echo "  6. Configure MEGAMIND to push to http://<SIDECAR_HOST>:9090 over Tailscale"
echo ""
echo "Test a schema: curl http://127.0.0.1:9090/schema/thatdeveloperguy"

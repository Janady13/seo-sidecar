#!/bin/bash
# ─────────────────────────────────────────────────────────────────────────────
# deploy-ga-update.sh — Update sidecar with GA integration
#
# Usage: sudo bash deploy-ga-update.sh
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

echo "═══ SEO Sidecar GA Integration Update ═══"

# ─── Check we're running as root/sudo ───────────────────────────────────────
if [ "$EUID" -ne 0 ]; then
    echo "Error: This script must be run with sudo"
    exit 1
fi

# ─── Backup current sidecar ─────────────────────────────────────────────────
echo "[1/4] Backing up current sidecar..."
if [ -f /opt/seo-sidecar/sidecar.py ]; then
    cp /opt/seo-sidecar/sidecar.py /opt/seo-sidecar/sidecar.py.backup-$(date +%Y%m%d-%H%M%S)
    echo "  ✓ Backup created"
else
    echo "  ⚠ No existing sidecar found"
fi

# ─── Install updated sidecar ────────────────────────────────────────────────
echo "[2/4] Installing updated sidecar with GA endpoints..."
if [ -f /tmp/sidecar.py ]; then
    cp /tmp/sidecar.py /opt/seo-sidecar/sidecar.py
    chown www-data:www-data /opt/seo-sidecar/sidecar.py
    chmod 600 /opt/seo-sidecar/sidecar.py
    echo "  ✓ Sidecar updated"
else
    echo "  ✗ /tmp/sidecar.py not found"
    echo "    Copy the updated sidecar.py to /tmp first"
    exit 1
fi

# ─── Restart service ────────────────────────────────────────────────────────
echo "[3/4] Restarting seo-sidecar service..."
systemctl restart seo-sidecar
sleep 2
if systemctl is-active --quiet seo-sidecar; then
    echo "  ✓ Service running"
else
    echo "  ✗ Service failed to start"
    systemctl status seo-sidecar --no-pager
    exit 1
fi

# ─── Test GA endpoint ───────────────────────────────────────────────────────
echo "[4/4] Testing GA endpoint..."
TOKEN=$(grep -oP 'SEO_AUTH_TOKEN=\K[^\s]+' /etc/systemd/system/seo-sidecar.service 2>/dev/null || echo "CHANGE_ME_DEFAULT")

GA_RESPONSE=$(curl -s -w "%{http_code}" -o /tmp/ga-test.json \
    -H "Authorization: Bearer $TOKEN" \
    http://127.0.0.1:9090/ga/report-latest 2>/dev/null)

if [ "$GA_RESPONSE" = "200" ]; then
    echo "  ✓ GA endpoint working"
    GENERATED=$(cat /tmp/ga-test.json | python3 -c "import json,sys; print(json.load(sys.stdin).get('generatedAt','unknown'))" 2>/dev/null)
    echo "  ✓ Latest report: $GENERATED"
elif [ "$GA_RESPONSE" = "404" ]; then
    echo "  ⚠ GA report not found - run ga4-report-pack.py first"
else
    echo "  ✗ GA endpoint returned: $GA_RESPONSE"
fi

echo ""
echo "═══ Update Complete ═══"
echo ""
echo "New endpoints available:"
echo "  GET /ga/report-latest     - Full GA report (requires auth)"
echo "  GET /ga/domain/{domain}   - Single domain data"
echo "  GET /ga/top-pages/{domain} - Top pages for domain"
echo ""
echo "Test: curl -H 'Authorization: Bearer $TOKEN' http://127.0.0.1:9090/ga/report-latest"

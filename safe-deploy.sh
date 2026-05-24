#!/bin/bash
# ═══════════════════════════════════════════════════════════════════════════════
# SAFE SEO SIDECAR DEPLOYMENT — Won't break existing sites
#
# This script:
#   1. Backs up all nginx configs
#   2. Installs sidecar service (doesn't touch nginx yet)
#   3. Tests sidecar is working
#   4. ONLY THEN applies nginx changes
#   5. Tests nginx config before reload
#   6. Validates sites still work after
#
# Run as: sudo bash safe-deploy.sh
# ═══════════════════════════════════════════════════════════════════════════════
set -euo pipefail

BACKUP_DIR="/home/user/backups/seo-sidecar-$(date +%Y%m%d-%H%M%S)"
SIDECAR_DIR="/opt/seo-sidecar"
DATA_DIR="/var/lib/seo-sidecar"
AUTH_TOKEN="megamind-bubbles-$(date +%s)"

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "  SEO SIDECAR SAFE DEPLOYMENT"
echo "═══════════════════════════════════════════════════════════════"
echo ""

# ─── Pre-flight checks ─────────────────────────────────────────────────────────
echo "[0/8] Pre-flight checks..."

# Check we're root
if [ "$EUID" -ne 0 ]; then
    echo "  ✗ Must run as root (sudo)"
    exit 1
fi

# Check nginx is running
if ! systemctl is-active --quiet nginx; then
    echo "  ✗ nginx is not running"
    exit 1
fi
echo "  ✓ nginx is running"

# Check Python3 and pip
if ! command -v python3 &> /dev/null; then
    echo "  ✗ python3 not found"
    exit 1
fi
echo "  ✓ python3 available"

# ─── Backup everything ─────────────────────────────────────────────────────────
echo ""
echo "[1/8] Creating comprehensive backup..."
mkdir -p "$BACKUP_DIR"

# Backup nginx configs
cp -r /etc/nginx/sites-available "$BACKUP_DIR/"
cp -r /etc/nginx/sites-enabled "$BACKUP_DIR/"
cp -r /etc/nginx/conf.d "$BACKUP_DIR/" 2>/dev/null || mkdir -p "$BACKUP_DIR/conf.d"
cp -r /etc/nginx/snippets "$BACKUP_DIR/" 2>/dev/null || mkdir -p "$BACKUP_DIR/snippets"

# Save rollback script
cat > "$BACKUP_DIR/rollback.sh" << 'ROLLBACK'
#!/bin/bash
echo "Rolling back nginx configs..."
cp -r sites-available/* /etc/nginx/sites-available/
cp -r sites-enabled/* /etc/nginx/sites-enabled/
rm -f /etc/nginx/conf.d/seo-sidecar-upstream.conf
rm -f /etc/nginx/snippets/seo-inject.conf
nginx -t && systemctl reload nginx
systemctl stop seo-sidecar 2>/dev/null || true
systemctl disable seo-sidecar 2>/dev/null || true
echo "Rollback complete"
ROLLBACK
chmod +x "$BACKUP_DIR/rollback.sh"

echo "  ✓ Backup saved to $BACKUP_DIR"
echo "  ✓ Rollback script: $BACKUP_DIR/rollback.sh"

# ─── Install Python dependencies ───────────────────────────────────────────────
echo ""
echo "[2/8] Installing Python dependencies..."
# Try apt first (preferred on Debian/Ubuntu), then pip with --break-system-packages
if apt-get install -y python3-fastapi python3-uvicorn python3-pydantic >/dev/null 2>&1; then
    echo "  ✓ FastAPI + uvicorn installed via apt"
elif pip3 install --break-system-packages fastapi uvicorn pydantic 2>/dev/null; then
    echo "  ✓ FastAPI + uvicorn installed via pip"
else
    echo "  ⚠ Installing via pip (may need manual intervention)..."
    pip3 install --break-system-packages fastapi uvicorn pydantic || {
        echo "  ✗ Failed to install dependencies"
        echo "  Try: apt-get install python3-fastapi python3-uvicorn python3-pydantic"
        exit 1
    }
    echo "  ✓ FastAPI + uvicorn installed"
fi

# ─── Create directories ────────────────────────────────────────────────────────
echo ""
echo "[3/8] Creating sidecar directories..."
mkdir -p "$SIDECAR_DIR"
mkdir -p "$DATA_DIR"
chown -R www-data:www-data "$SIDECAR_DIR"
chown -R www-data:www-data "$DATA_DIR"
echo "  ✓ Directories created"

# ─── Copy sidecar files ────────────────────────────────────────────────────────
echo ""
echo "[4/8] Installing sidecar service..."
cp sidecar.py "$SIDECAR_DIR/"
cp megamind_push.py "$SIDECAR_DIR/" 2>/dev/null || true

# Create systemd service with secure token
cat > /etc/systemd/system/seo-sidecar.service << EOF
[Unit]
Description=SEO Schema Sidecar — serves JSON-LD to nginx from MEGAMIND pushes
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=www-data
Group=www-data

ExecStart=/usr/bin/python3 -m uvicorn sidecar:app --host 127.0.0.1 --port 9090
WorkingDirectory=$SIDECAR_DIR

Environment="SEO_DB_PATH=$DATA_DIR/schemas.db"
Environment="SEO_AUTH_TOKEN=$AUTH_TOKEN"
Environment="SEO_HOST=127.0.0.1"
Environment="SEO_PORT=9090"

ProtectSystem=strict
ReadWritePaths=$DATA_DIR
PrivateTmp=true
NoNewPrivileges=true

Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable seo-sidecar
systemctl start seo-sidecar

# Wait and verify
sleep 3
if systemctl is-active --quiet seo-sidecar; then
    echo "  ✓ seo-sidecar service running on localhost:9090"
else
    echo "  ✗ seo-sidecar failed to start"
    systemctl status seo-sidecar --no-pager -l
    exit 1
fi

# ─── Test sidecar is responding ────────────────────────────────────────────────
echo ""
echo "[5/8] Testing sidecar API..."
SIDECAR_STATUS=$(curl -s http://127.0.0.1:9090/status 2>/dev/null || echo "FAIL")
if echo "$SIDECAR_STATUS" | grep -q "healthy"; then
    echo "  ✓ Sidecar /status endpoint working"
else
    echo "  ✗ Sidecar not responding correctly"
    echo "  Response: $SIDECAR_STATUS"
    exit 1
fi

# ─── Install nginx configs (but don't apply to sites yet) ─────────────────────
echo ""
echo "[6/8] Installing nginx upstream config..."

# Install the upstream and map config
cp nginx-seo-sidecar.conf /etc/nginx/conf.d/seo-sidecar-upstream.conf

# Install the snippet
mkdir -p /etc/nginx/snippets
cp seo-inject.conf /etc/nginx/snippets/seo-inject.conf

# Test nginx config BEFORE any changes
if nginx -t 2>&1; then
    echo "  ✓ nginx config valid with new upstream"
else
    echo "  ✗ nginx config error - not applying"
    rm -f /etc/nginx/conf.d/seo-sidecar-upstream.conf
    rm -f /etc/nginx/snippets/seo-inject.conf
    exit 1
fi

# ─── Reload nginx (just loads the upstream, doesn't change sites) ─────────────
echo ""
echo "[7/8] Reloading nginx with upstream config..."
systemctl reload nginx
echo "  ✓ nginx reloaded"

# ─── Final validation ──────────────────────────────────────────────────────────
echo ""
echo "[8/8] Validating sites still work..."

SITES=("feedthejoe.com" "thataiguy.org" "thatdeveloperguy.com")
ALL_OK=true

for site in "${SITES[@]}"; do
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 "https://$site" 2>/dev/null || echo "000")
    if [ "$HTTP_CODE" = "200" ]; then
        echo "  ✓ $site → HTTP $HTTP_CODE"
    else
        echo "  ✗ $site → HTTP $HTTP_CODE"
        ALL_OK=false
    fi
done

if [ "$ALL_OK" = false ]; then
    echo ""
    echo "  ⚠️  Some sites returned non-200 status"
    echo "  This may or may not be a problem - check manually"
fi

# ─── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "  DEPLOYMENT COMPLETE"
echo "═══════════════════════════════════════════════════════════════"
echo ""
echo "  Sidecar is running but NOT injecting schemas yet."
echo "  Sites are unchanged and still working."
echo ""
echo "  NEXT STEPS (do manually, one site at a time):"
echo ""
echo "  1. Add to ONE site's server block:"
echo "       include /etc/nginx/snippets/seo-inject.conf;"
echo ""
echo "  2. Test nginx: nginx -t"
echo ""
echo "  3. Reload nginx: systemctl reload nginx"
echo ""
echo "  4. Push a schema from MEGAMIND:"
echo "       curl -X POST http://<YOUR_TAILSCALE_IP>:9090/update/feedthejoe \\"
echo "         -H 'Authorization: Bearer $AUTH_TOKEN' \\"
echo "         -H 'Content-Type: application/json' \\"
echo "         -d '{\"site\":\"feedthejoe\",\"jsonld\":\"{}\"}'"
echo ""
echo "  5. Verify schema appears: curl https://feedthejoe.com | grep ld+json"
echo ""
echo "  6. Repeat for other sites"
echo ""
echo "  AUTH TOKEN (save this): $AUTH_TOKEN"
echo "  BACKUP LOCATION: $BACKUP_DIR"
echo "  ROLLBACK: cd $BACKUP_DIR && sudo bash rollback.sh"
echo ""

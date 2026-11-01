#!/usr/bin/env bash
# نصب روزنگار روی اوبونتو (۲۲.۰۴ یا ۲۴.۰۴)
# استفاده:  sudo ./install.sh rooznegar.example.ir
set -euo pipefail

DOMAIN="${1:-}"
HERE="$(cd "$(dirname "$0")" && pwd)"
[ "$(id -u)" = 0 ] || { echo "با sudo اجرا کنید"; exit 1; }
[ -n "$DOMAIN" ] || { echo "استفاده: sudo ./install.sh rooznegar.example.ir"; exit 1; }

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) BIN="$HERE/rooznegar-linux-amd64" ;;
  aarch64|arm64) BIN="$HERE/rooznegar-linux-arm64" ;;
  *) echo "معماری پشتیبانی نمی‌شود: $ARCH"; exit 1 ;;
esac

echo "→ برنامه"
systemctl stop rooznegar 2>/dev/null || true
install -m 0755 "$BIN" /usr/local/bin/rooznegar
id rooznegar >/dev/null 2>&1 || useradd --system --home /var/lib/rooznegar --shell /usr/sbin/nologin rooznegar
install -d -o rooznegar -g rooznegar -m 0750 /var/lib/rooznegar
install -d -o root -g rooznegar -m 0750 /etc/rooznegar

if [ ! -f /etc/rooznegar/config.json ]; then
  sed "s#https://rooznegar.example.ir#https://$DOMAIN#" "$HERE/config.example.json" > /etc/rooznegar/config.json
  chown root:rooznegar /etc/rooznegar/config.json
  chmod 0640 /etc/rooznegar/config.json
  echo "  config ساخته شد: /etc/rooznegar/config.json (توکن‌ها و کلید را در آن بگذارید)"
else
  echo "  config موجود دست نخورد"
fi

install -m 0640 -o root -g rooznegar "$HERE/seed.json" /etc/rooznegar/seed.json

echo "→ سرویس"
cat > /etc/systemd/system/rooznegar.service <<EOF
[Unit]
Description=Rooznegar team bot
After=network-online.target
Wants=network-online.target

[Service]
User=rooznegar
Group=rooznegar
ExecStart=/usr/local/bin/rooznegar serve -config /etc/rooznegar/config.json
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/rooznegar

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable rooznegar >/dev/null

echo "→ nginx"
if ! command -v nginx >/dev/null; then apt-get update -qq && apt-get install -y -qq nginx; fi
cat > /etc/nginx/sites-available/rooznegar <<EOF
server {
    listen 80;
    server_name $DOMAIN;
    client_max_body_size 60m;
    location / {
        proxy_pass http://127.0.0.1:8095;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_read_timeout 90s;
    }
}
EOF
ln -sf /etc/nginx/sites-available/rooznegar /etc/nginx/sites-enabled/rooznegar
nginx -t && systemctl reload nginx

echo "→ SSL"
if ! command -v certbot >/dev/null; then apt-get install -y -qq certbot python3-certbot-nginx || true; fi
if command -v certbot >/dev/null; then
  certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --register-unsafely-without-email --redirect \
    || echo "  SSL گرفته نشد. اول رکورد A دامنه را روی آی‌پی این سرور بگذارید، بعد: sudo certbot --nginx -d $DOMAIN"
fi

echo
echo "نصب شد. قدم‌های بعدی (سرویس هنوز خاموش است):"
echo "  ۱. sudo nano /etc/rooznegar/config.json   ← توکن ربات‌ها و کلید API"
echo "  ۲. sudo -u rooznegar rooznegar seed /etc/rooznegar/seed.json"
echo "  ۳. sudo -u rooznegar rooznegar user <نام‌کاربری> admin <نام شما>"
echo "  ۴. sudo -u rooznegar rooznegar check"
echo "  ۵. sudo systemctl start rooznegar   و بعد  https://$DOMAIN"

#!/bin/sh
# One-time setup of automatic updates for rooznegar (run as root, inside the unzipped release folder).
# After this, new releases on GitHub are installed by themselves within ~5 minutes,
# and a release that doesn't come up healthy is rolled back automatically.
set -e
[ "$(id -u)" = 0 ] || { echo "با sudo اجرا کنید"; exit 1; }
CFG=/etc/rooznegar/config.json
REPO=${REPO:-shahcheshme70-cando/rooznegar}
case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "معماری ناشناخته: $(uname -m)"; exit 1 ;;
esac
HERE=$(cd "$(dirname "$0")" && pwd)

echo "→ نصب این نسخه"
systemctl stop rooznegar || true
install -m 0755 "$HERE/rooznegar-linux-$ARCH" /usr/local/bin/rooznegar
chown -R rooznegar:rooznegar /var/lib/rooznegar

echo "→ کلید فقط‌خواندنی GitHub (برای مخزن $REPO)"
if [ -z "$TOKEN" ]; then
  printf "توکن را بچسبانید و Enter بزنید (دیده نمی‌شود): "
  stty -echo; read -r TOKEN; stty echo; echo
fi
[ -n "$TOKEN" ] || { echo "توکن خالی است"; exit 1; }
python3 - "$CFG" "$REPO" "$TOKEN" <<'PY'
import json, sys
path, repo, token = sys.argv[1:4]
with open(path) as f: c = json.load(f)
c["update_repo"], c["update_token"] = repo, token
with open(path, "w") as f: json.dump(c, f, ensure_ascii=False, indent=2)
PY
chown root:rooznegar "$CFG"; chmod 0640 "$CFG"

echo "→ سرویس به‌روزرسانی (هر ۵ دقیقه)"
cat > /etc/systemd/system/rooznegar-update.service <<UNIT
[Unit]
Description=Rooznegar self-update
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/rooznegar self-update -config $CFG
UNIT
cat > /etc/systemd/system/rooznegar-update.timer <<UNIT
[Unit]
Description=Check for rooznegar updates every 5 minutes

[Timer]
OnBootSec=2min
OnUnitActiveSec=5min
RandomizedDelaySec=30

[Install]
WantedBy=timers.target
UNIT
systemctl daemon-reload
systemctl start rooznegar
systemctl enable --now rooznegar-update.timer >/dev/null

# bigger files for "ارسال برای همه"
if [ -f /etc/nginx/sites-available/rooznegar ]; then
  sed -i 's/client_max_body_size 20m;/client_max_body_size 60m;/' /etc/nginx/sites-available/rooznegar
  nginx -t >/dev/null 2>&1 && systemctl reload nginx
fi

echo "→ آزمایش"
sleep 3
/usr/local/bin/rooznegar version
/usr/local/bin/rooznegar self-update -config "$CFG" && echo "✓ به‌روزرسانی خودکار راه افتاد."

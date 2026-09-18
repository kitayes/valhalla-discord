#!/usr/bin/env bash
# Поднимает временный Cloudflare-туннель к локальному мини-аппу и прописывает
# выданный адрес в .env (WEB_APP_URL).
#
# Адреса trycloudflare одноразовые: каждый запуск даёт новый домен, а Telegram
# помнит тот, что записан в настройках бота. Поэтому скрипт не только поднимает
# туннель, но и обновляет .env — иначе кнопка «Открыть приложение» будет вести
# на мёртвую ссылку.
#
# Постоянный адрес так не получить — см. README-секцию про named tunnel.
set -euo pipefail

PORT="${1:-8088}"
ENV_FILE="$(dirname "$0")/../.env"
LOG="$(mktemp -t cloudflared-XXXXXX.log)"

command -v cloudflared >/dev/null || { echo "cloudflared не установлен"; exit 1; }

echo "Поднимаю туннель на 127.0.0.1:${PORT}..."
cloudflared tunnel --url "http://127.0.0.1:${PORT}" >"$LOG" 2>&1 &
TUNNEL_PID=$!
trap 'kill $TUNNEL_PID 2>/dev/null || true' EXIT

# Ждём, пока cloudflared напечатает выданный домен (обычно 5-10 секунд).
URL=""
for _ in $(seq 1 60); do
    URL=$(grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' "$LOG" | head -1 || true)
    [ -n "$URL" ] && break
    kill -0 $TUNNEL_PID 2>/dev/null || { echo "cloudflared упал:"; cat "$LOG"; exit 1; }
    sleep 1
done
[ -n "$URL" ] || { echo "Не дождался адреса. Лог: $LOG"; exit 1; }

echo "Адрес: ${URL}"

if [ -f "$ENV_FILE" ]; then
    if grep -q '^WEB_APP_URL=' "$ENV_FILE"; then
        sed -i "s|^WEB_APP_URL=.*|WEB_APP_URL=${URL}/app|" "$ENV_FILE"
    else
        printf '\nWEB_APP_URL=%s/app\n' "$URL" >>"$ENV_FILE"
    fi
    echo "WEB_APP_URL обновлён в .env"
fi

cat <<EOF

Готово. Мини-апп: ${URL}/app

Дальше:
  1. Перезапусти бота, чтобы он подхватил новый WEB_APP_URL.
  2. В @BotFather пропиши этот же адрес: /setmenubutton (или Bot Settings ->
     Menu Button), иначе кнопка меню останется на старом домене.

Туннель живёт, пока запущен этот скрипт. Ctrl+C — закрыть.
EOF

wait $TUNNEL_PID

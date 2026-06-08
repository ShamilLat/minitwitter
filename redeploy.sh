#!/usr/bin/env bash
# Выкатка новой версии в прод одной командой (с локальной машины).
# Использование: bash redeploy.sh
set -e
git push origin main
ssh -o BatchMode=yes sam@81.26.177.214 '
  cd /opt/minitwitter &&
  sudo git pull &&
  sudo docker compose up -d --build
'
echo "Deployed. Open http://81.26.177.214"

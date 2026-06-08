# Деплой minitwitter в Yandex Cloud (для бекендера, который не деплоил сайты)

Идея: одна виртуалка (Compute Cloud) + Docker Compose. Внутри — наш Go-сервис и Postgres.
Выкатка новой версии = `git pull && docker compose up -d --build`.

---

## Шаг 1. Создать виртуалку

1. Зайти в https://console.cloud.yandex.ru → создать **платёжный аккаунт** (платится рублями с карты).
2. Compute Cloud → **Создать ВМ**:
   - Образ: **Ubuntu 22.04**
   - vCPU 2 / RAM 2 ГБ (хватит с запасом; можно 2/2 burstable — дешевле)
   - Диск: 20 ГБ SSD
   - **Публичный IP: автоматически** (обязательно — иначе сайт не открыть)
   - SSH: вписать **логин** (например `sam`) и **свой публичный SSH-ключ**
     - Ключ генерится локально: `ssh-keygen -t ed25519` → содержимое `~/.ssh/id_ed25519.pub` вставить в форму.
3. Создать. Записать **публичный IP** (например `158.160.x.x`).

## Шаг 2. Открыть порты (Security Group)

В сетевых настройках ВМ разрешить **входящие**:
- TCP **22** (SSH) — с твоего IP или 0.0.0.0/0
- TCP **80** (HTTP) — с 0.0.0.0/0

## Шаг 3. Зайти и поставить Docker

```bash
ssh sam@158.160.x.x      # твой логин и IP

# ставим Docker одной командой
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER
newgrp docker             # применить группу без перелогина
```

## Шаг 4. Забрать код и запустить

Код лежит в git. Варианты доставки:

**A. Через GitHub (рекомендую):**
```bash
# на сервере
git clone https://github.com/<твой-логин>/minitwitter.git
cd minitwitter
docker compose up -d --build
```

**B. Без GitHub — закинуть прямо с ноута:**
```bash
# на ЛОКАЛЬНОЙ машине, из папки проекта
rsync -az --exclude .git ./ sam@158.160.x.x:~/minitwitter/
# затем на сервере:
cd ~/minitwitter && docker compose up -d --build
```

Проверка:
```bash
curl localhost/healthz        # -> ok
```
Открыть в браузере: **http://158.160.x.x** — должна открыться лента.

## Шаг 5. Выкатка новой версии (каждый раз)

```bash
ssh sam@158.160.x.x
cd minitwitter
git pull                       # или rsync с ноута
docker compose up -d --build   # пересоберёт app, БД останется
```

Логи: `docker compose logs -f app`
Остановить: `docker compose down` (данные сохранятся в volume `pgdata`)

---

## Заметки
- Данные Postgres живут в docker volume `pgdata` — переживают пересборку и перезапуск.
- HTTPS пока нет (только http). Добавить можно через Caddy за 5 минут позже — попроси.
- Стоимость: маленькая ВМ ~ несколько сотен рублей в месяц.

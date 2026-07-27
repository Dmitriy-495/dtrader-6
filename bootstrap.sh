#!/bin/bash
# bootstrap.sh — первичная настройка VDS под dtrader-6
# Запускать через: ssh msk 'bash -s' < bootstrap.sh
#                  ssh sgp 'bash -s' < bootstrap.sh
#
# Идемпотентен: можно запускать повторно, ничего не сломает.

set -euo pipefail

GO_VERSION="1.22.3"
APP_USER="$(whoami)"
APP_ROOT="/home/${APP_USER}/dtrader-6"

echo "=================================================="
echo " Bootstrap dtrader-6 on $(hostname)"
echo " User: ${APP_USER}"
echo "=================================================="

# -------------------------------------------------------
# 1. Системные пакеты
# -------------------------------------------------------
echo "--> Обновление apt и установка базовых пакетов"
sudo apt-get update -qq
sudo apt-get install -y -qq \
    build-essential \
    curl \
    wget \
    git \
    ufw \
    redis-server \
    jq \
    net-tools \
    > /dev/null

# -------------------------------------------------------
# 2. Go 1.22.3 (если не установлен нужной версии)
# -------------------------------------------------------
CURRENT_GO_VERSION=""
if command -v go >/dev/null 2>&1; then
    CURRENT_GO_VERSION="$(go version | awk '{print $3}' | sed 's/go//')"
fi

if [[ "${CURRENT_GO_VERSION}" != "${GO_VERSION}" ]]; then
    echo "--> Установка Go ${GO_VERSION} (текущая: ${CURRENT_GO_VERSION:-нет})"
    ARCH="amd64"
    GO_TARBALL="go${GO_VERSION}.linux-${ARCH}.tar.gz"
    cd /tmp
    curl -fsSL -O "https://go.dev/dl/${GO_TARBALL}"
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf "${GO_TARBALL}"
    rm -f "${GO_TARBALL}"
else
    echo "--> Go ${GO_VERSION} уже установлен, пропуск"
fi

# PATH для go — кладём в профиль, если ещё не добавлено
if ! grep -q "/usr/local/go/bin" ~/.profile 2>/dev/null; then
    echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.profile
fi
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin

echo "--> $(go version)"

# -------------------------------------------------------
# 3. Redis — включить автозапуск, привязать к localhost
# -------------------------------------------------------
echo "--> Настройка Redis"
sudo sed -i 's/^bind .*/bind 127.0.0.1 ::1/' /etc/redis/redis.conf
sudo sed -i 's/^# maxmemory-policy.*/maxmemory-policy allkeys-lru/' /etc/redis/redis.conf
sudo systemctl enable redis-server > /dev/null 2>&1
sudo systemctl restart redis-server
echo "--> Redis: $(redis-cli ping)"

# Разрешаем overcommit памяти — иначе BGSAVE (fork) может падать при нехватке
# памяти, что критично на серверах с малым объёмом RAM (напр. 2GB VDS)
if ! grep -q "^vm.overcommit_memory" /etc/sysctl.conf 2>/dev/null; then
    echo "vm.overcommit_memory = 1" | sudo tee -a /etc/sysctl.conf > /dev/null
fi
sudo sysctl vm.overcommit_memory=1 > /dev/null
echo "--> vm.overcommit_memory установлен в 1"

# -------------------------------------------------------
# 4. Firewall (UFW)
# -------------------------------------------------------
echo "--> Настройка UFW"
sudo ufw allow OpenSSH > /dev/null
sudo ufw allow 9000/tcp comment 'dtrader ws-server' > /dev/null
# Redis (6379) НЕ открываем наружу — только localhost, доступ через SSH туннель при необходимости
sudo ufw --force enable > /dev/null
echo "--> UFW статус:"
sudo ufw status verbose

# -------------------------------------------------------
# 5. Структура папок под деплой
# -------------------------------------------------------
echo "--> Создание структуры папок в ${APP_ROOT}"
# ВАЖНО: bot и ws-server оба грузят config.yaml по относительному пути
# "config.yaml" (захардкожено в коде) — поэтому каждому нужна СВОЯ рабочая
# директория, иначе конфиги будут конфликтовать в одной общей bin/.
mkdir -p "${APP_ROOT}"/{bin/bot,bin/ws-server,releases,shared/config,logs}

# -------------------------------------------------------
# 6. systemd unit-файлы (шаблоны, .env подключается отдельно)
# -------------------------------------------------------
echo "--> Установка systemd unit-файлов"

sudo tee /etc/systemd/system/dtrader-bot.service > /dev/null <<EOF
[Unit]
Description=DTrader 6 - Bot (Gate.io -> Redis)
After=network-online.target redis-server.service
Wants=network-online.target
Requires=redis-server.service

[Service]
Type=simple
User=${APP_USER}
WorkingDirectory=${APP_ROOT}/bin/bot
ExecStart=${APP_ROOT}/bin/bot/dtrader-bot
EnvironmentFile=${APP_ROOT}/shared/config/bot.env
Restart=on-failure
RestartSec=3
StandardOutput=append:${APP_ROOT}/logs/bot.log
StandardError=append:${APP_ROOT}/logs/bot.error.log

# Базовое ужесточение
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=${APP_ROOT}/logs
ProtectHome=false

[Install]
WantedBy=multi-user.target
EOF

sudo tee /etc/systemd/system/dtrader-ws.service > /dev/null <<EOF
[Unit]
Description=DTrader 6 - WS Server (Redis -> WebSocket clients)
After=network-online.target redis-server.service dtrader-bot.service
Wants=network-online.target
Requires=redis-server.service

[Service]
Type=simple
User=${APP_USER}
WorkingDirectory=${APP_ROOT}/bin/ws-server
ExecStart=${APP_ROOT}/bin/ws-server/dtrader-ws
EnvironmentFile=${APP_ROOT}/shared/config/ws-server.env
Restart=on-failure
RestartSec=3
StandardOutput=append:${APP_ROOT}/logs/ws-server.log
StandardError=append:${APP_ROOT}/logs/ws-server.error.log

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=${APP_ROOT}/logs
ProtectHome=false

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload

echo "=================================================="
echo " Bootstrap завершён на $(hostname)"
echo "=================================================="
echo ""
echo "ДАЛЬШЕ ВРУЧНУЮ (один раз):"
echo "  1. Создать ${APP_ROOT}/shared/config/bot.env"
echo "  2. Создать ${APP_ROOT}/shared/config/ws-server.env"
echo "  3. Положить config.yaml для bot и ws-server в ${APP_ROOT}/shared/config/"
echo "  (см. env.example, который пришлю отдельно — деплой-скрипт это не делает"
echo "   автоматически из соображений безопасности секретов)"
echo ""
echo "Затем деплой бинарников выполняется скриптом deploy.sh с локальной машины."
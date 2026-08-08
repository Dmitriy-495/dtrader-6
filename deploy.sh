#!/bin/bash
# deploy.sh — сборка локально + деплой на msk и sgp по SSH
#
# Использование:
#   ./deploy.sh                  — деплой bot + ws-server + analyzer на оба сервера
#   ./deploy.sh bot               — деплой только bot на оба сервера
#   ./deploy.sh ws                 — деплой только ws-server на оба сервера
#   ./deploy.sh analyzer            — деплой только analyzer на оба сервера
#   ./deploy.sh bot msk             — деплой только bot только на msk
#   ./deploy.sh --config-only        — обновить только config.yaml (без пересборки)
#
# Требует: ~/.ssh/config с алиасами msk и sgp, локально установленный Go 1.22.3+
#
# Структура на сервере (создаётся bootstrap.sh):
#   ~/dtrader-6/bin/bot/config.yaml + dtrader-bot              (WorkingDirectory для systemd)
#   ~/dtrader-6/bin/ws-server/config.yaml + dtrader-ws
#   ~/dtrader-6/bin/analyzer/config.yaml + dtrader-analyzer
#   ~/dtrader-6/shared/config/{bot.env,ws-server.env,analyzer.env}  (секреты, заполняются вручную)

set -uo pipefail
# Примечание: -e сознательно НЕ используется здесь — при нестабильном канале
# (msk из России) scp может рвать соединение, и мы хотим сами обработать это
# через retry, а не падать сразу.

# scp_retry <src> <dst> [max_attempts]
# Повторяет scp с сжатием при обрыве соединения — актуально для нестабильного
# канала до msk. Каждая попытка — заново с начала файла (scp не умеет докачку),
# но при обрыве на 90%+ повторная попытка на сжатом канале обычно быстрее.
scp_retry() {
    local src="$1" dst="$2" max_attempts="${3:-4}"
    local attempt=1
    while (( attempt <= max_attempts )); do
        if scp -C -o ServerAliveInterval=5 -o ServerAliveCountMax=3 "${src}" "${dst}"; then
            return 0
        fi
        echo "  ⚠️  Попытка ${attempt}/${max_attempts} не удалась (обрыв соединения), повтор через 3с..."
        sleep 3
        ((attempt++))
    done
    echo "  ❌ scp не удался после ${max_attempts} попыток: ${src} -> ${dst}"
    return 1
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BOT_DIR="${REPO_ROOT}/bot"
WS_DIR="${REPO_ROOT}/ws-server"
ANALYZER_DIR="${REPO_ROOT}/analyzer"
REMOTE_APP_ROOT="dtrader-6"
HOSTS=(msk sgp)
BUILD_DIR="${REPO_ROOT}/.build"

TARGET="${1:-all}"          # all | bot | ws | analyzer | --config-only
ONLY_HOST="${2:-}"          # если задан — деплоим только на этот хост

if [[ -n "${ONLY_HOST}" ]]; then
    HOSTS=("${ONLY_HOST}")
fi

# -------------------------------------------------------
# Проверка окружения
# -------------------------------------------------------
if ! command -v go >/dev/null 2>&1; then
    echo "❌ Go не найден локально. Установи Go 1.22.3+."
    exit 1
fi

mkdir -p "${BUILD_DIR}"

deploy_bot="false"
deploy_ws="false"
deploy_analyzer="false"
case "${TARGET}" in
    all) deploy_bot="true"; deploy_ws="true"; deploy_analyzer="true" ;;
    bot) deploy_bot="true" ;;
    ws)  deploy_ws="true" ;;
    analyzer) deploy_analyzer="true" ;;
    --config-only) deploy_bot="true"; deploy_ws="true"; deploy_analyzer="true" ;;
    *) echo "❌ Неизвестный таргет: ${TARGET} (используй: all | bot | ws | analyzer | --config-only)"; exit 1 ;;
esac

# -------------------------------------------------------
# Сборка (пропускается для --config-only)
# -------------------------------------------------------
if [[ "${TARGET}" != "--config-only" ]]; then
    if [[ "${deploy_bot}" == "true" ]]; then
        echo "🔨 Сборка bot (linux/amd64)..."
        (cd "${BOT_DIR}" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
            go build -ldflags="-s -w" -o "${BUILD_DIR}/dtrader-bot" ./cmd)
        echo "✅ bot собран: $(du -h "${BUILD_DIR}/dtrader-bot" | cut -f1)"
    fi
    if [[ "${deploy_ws}" == "true" ]]; then
        echo "🔨 Сборка ws-server (linux/amd64)..."
        (cd "${WS_DIR}" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
            go build -ldflags="-s -w" -o "${BUILD_DIR}/dtrader-ws" ./cmd)
        echo "✅ ws-server собран: $(du -h "${BUILD_DIR}/dtrader-ws" | cut -f1)"
    fi
    if [[ "${deploy_analyzer}" == "true" ]]; then
        echo "🔨 Сборка analyzer (linux/amd64)..."
        (cd "${ANALYZER_DIR}" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
            go build -ldflags="-s -w" -o "${BUILD_DIR}/dtrader-analyzer" ./cmd)
        echo "✅ analyzer собран: $(du -h "${BUILD_DIR}/dtrader-analyzer" | cut -f1)"
    fi
fi

# -------------------------------------------------------
# Деплой на каждый хост
# -------------------------------------------------------
deploy_to_host() {
    local host="$1"
    local remote="${REMOTE_APP_ROOT}"
    echo ""
    echo "🚀 Деплой на ${host}..."

    if [[ "${deploy_bot}" == "true" ]]; then
        scp_retry "${BOT_DIR}/config.yaml" "${host}:${remote}/bin/bot/config.yaml.new" || return 1
    fi
    if [[ "${deploy_ws}" == "true" ]]; then
        scp_retry "${WS_DIR}/config.yaml" "${host}:${remote}/bin/ws-server/config.yaml.new" || return 1
    fi
    if [[ "${deploy_analyzer}" == "true" ]]; then
        scp_retry "${ANALYZER_DIR}/config.yaml" "${host}:${remote}/bin/analyzer/config.yaml.new" || return 1
    fi

    if [[ "${TARGET}" == "--config-only" ]]; then
        ssh "${host}" bash -s <<EOF
set -e
[[ -f ${remote}/bin/bot/config.yaml.new ]] && mv ${remote}/bin/bot/config.yaml.new ${remote}/bin/bot/config.yaml
[[ -f ${remote}/bin/ws-server/config.yaml.new ]] && mv ${remote}/bin/ws-server/config.yaml.new ${remote}/bin/ws-server/config.yaml
[[ -f ${remote}/bin/analyzer/config.yaml.new ]] && mv ${remote}/bin/analyzer/config.yaml.new ${remote}/bin/analyzer/config.yaml
sudo systemctl restart dtrader-bot dtrader-ws dtrader-analyzer
echo "  ✅ config.yaml обновлён, сервисы перезапущены"
EOF
        return
    fi

    if [[ "${deploy_bot}" == "true" ]]; then
        scp_retry "${BUILD_DIR}/dtrader-bot" "${host}:${remote}/bin/bot/dtrader-bot.new" || return 1
    fi
    if [[ "${deploy_ws}" == "true" ]]; then
        scp_retry "${BUILD_DIR}/dtrader-ws" "${host}:${remote}/bin/ws-server/dtrader-ws.new" || return 1
    fi
    if [[ "${deploy_analyzer}" == "true" ]]; then
        scp_retry "${BUILD_DIR}/dtrader-analyzer" "${host}:${remote}/bin/analyzer/dtrader-analyzer.new" || return 1
    fi

    ssh "${host}" bash -s <<EOF
set -e

if [[ "${deploy_bot}" == "true" ]]; then
    cd ${remote}/bin/bot
    chmod +x dtrader-bot.new
    mv dtrader-bot.new dtrader-bot
    [[ -f config.yaml.new ]] && mv config.yaml.new config.yaml
    sudo systemctl restart dtrader-bot
fi

if [[ "${deploy_ws}" == "true" ]]; then
    cd ${remote}/bin/ws-server
    chmod +x dtrader-ws.new
    mv dtrader-ws.new dtrader-ws
    [[ -f config.yaml.new ]] && mv config.yaml.new config.yaml
    sudo systemctl restart dtrader-ws
fi

if [[ "${deploy_analyzer}" == "true" ]]; then
    cd ${remote}/bin/analyzer
    chmod +x dtrader-analyzer.new
    mv dtrader-analyzer.new dtrader-analyzer
    [[ -f config.yaml.new ]] && mv config.yaml.new config.yaml
    sudo systemctl restart dtrader-analyzer
fi

sleep 1
if [[ "${deploy_bot}" == "true" ]]; then
    sudo systemctl is-active --quiet dtrader-bot && echo "  ✅ dtrader-bot: active" || { echo "  ❌ dtrader-bot: НЕ АКТИВЕН"; sudo journalctl -u dtrader-bot -n 15 --no-pager; }
fi
if [[ "${deploy_ws}" == "true" ]]; then
    sudo systemctl is-active --quiet dtrader-ws && echo "  ✅ dtrader-ws: active" || { echo "  ❌ dtrader-ws: НЕ АКТИВЕН"; sudo journalctl -u dtrader-ws -n 15 --no-pager; }
fi
if [[ "${deploy_analyzer}" == "true" ]]; then
    sudo systemctl is-active --quiet dtrader-analyzer && echo "  ✅ dtrader-analyzer: active" || { echo "  ❌ dtrader-analyzer: НЕ АКТИВЕН"; sudo journalctl -u dtrader-analyzer -n 15 --no-pager; }
fi
EOF
}

FAILED_HOSTS=()
for host in "${HOSTS[@]}"; do
    if ! deploy_to_host "${host}"; then
        FAILED_HOSTS+=("${host}")
        echo "  ⏭️  Пропускаю дальнейшие шаги для ${host}, перехожу к следующему серверу"
    fi
done

echo ""
echo "=================================================="
if (( ${#FAILED_HOSTS[@]} == 0 )); then
    echo "🎉 Деплой завершён на: ${HOSTS[*]}"
else
    echo "⚠️  Деплой завершён с ошибками на: ${FAILED_HOSTS[*]}"
    echo "    Успешно: $(comm -23 <(printf '%s\n' "${HOSTS[@]}" | sort) <(printf '%s\n' "${FAILED_HOSTS[@]}" | sort) | tr '\n' ' ')"
    echo "    Повтори для проблемных хостов: ./deploy.sh ${TARGET} <host>"
fi
echo "=================================================="
echo "Логи:    ssh <host> 'tail -f ~/${REMOTE_APP_ROOT}/logs/bot.log'"
echo "Статус:  ssh <host> 'systemctl status dtrader-bot dtrader-ws --no-pager'"
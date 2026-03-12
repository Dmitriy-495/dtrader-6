#!/bin/bash
export PATH=$PATH:/usr/local/go/bin
REPO=/home/tda495/code/dtrader/dtrader-6

while true; do
    cd $REPO
    git fetch origin master --quiet
    LOCAL=$(git rev-parse HEAD)
    REMOTE=$(git rev-parse origin/master)

    if [ "$LOCAL" != "$REMOTE" ]; then
        echo "🔄 $(date) Обнаружены изменения — анализируем..."
        CHANGED=$(git diff --name-only $LOCAL $REMOTE)
        echo "📝 Изменённые файлы:"
        echo "$CHANGED"

        git pull origin master

        REBUILD_BOT=false
        REBUILD_WS=false

        if echo "$CHANGED" | grep -q "^bot/"; then
            REBUILD_BOT=true
        fi
        if echo "$CHANGED" | grep -q "^ws-server/"; then
            REBUILD_WS=true
        fi

        if [ "$REBUILD_BOT" = true ]; then
            echo "🔨 Пересобираем bot..."
            cd $REPO/bot && go build -o bin/bot ./cmd/main.go
            echo "🔄 Перезапускаем dtrader-bot..."
            sudo systemctl restart dtrader-bot
            echo "✅ $(date) bot обновлён"
        fi

        if [ "$REBUILD_WS" = true ]; then
            echo "🔨 Пересобираем ws-server..."
            cd $REPO/ws-server && go build -o bin/ws-server ./cmd/main.go
            echo "🔄 Перезапускаем dtrader-ws-server..."
            sudo systemctl restart dtrader-ws-server
            echo "✅ $(date) ws-server обновлён"
        fi

        if [ "$REBUILD_BOT" = false ] && [ "$REBUILD_WS" = false ]; then
            echo "ℹ️ $(date) Изменения не затронули сервисы — перезапуск не нужен"
        fi
    fi

    sleep 30
done

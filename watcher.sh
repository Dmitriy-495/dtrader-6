#!/bin/bash
export PATH=$PATH:/usr/local/go/bin

REPO=/home/tda495/code/dtrader/dtrader-6

while true; do
    cd $REPO
    git fetch origin master --quiet

    LOCAL=$(git rev-parse HEAD)
    REMOTE=$(git rev-parse origin/master)

    if [ "$LOCAL" != "$REMOTE" ]; then
        echo "🔄 $(date) Обнаружены изменения — обновляемся..."
        git pull origin master
        echo "🔨 Пересобираем бота..."
        cd $REPO/bot && go build -o bin/bot ./cmd/main.go
        echo "🔄 Перезапускаем бота..."
        sudo systemctl restart dtrader-bot
        echo "✅ $(date) Бот обновлён и перезапущен"
    fi

    sleep 30
done

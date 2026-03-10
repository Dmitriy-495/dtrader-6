#!/bin/zsh
# deploy.sh — коммит и пуш изменений на GitHub
# Запускать вручную когда готов задеплоить: ./deploy.sh "описание изменений"

cd /home/tda/code/dtrader/dtrader-6

# Проверяем есть ли изменения
if [[ -z $(git status --porcelain) ]]; then
    echo "✅ Нет изменений для коммита"
    exit 0
fi

# Сообщение коммита — берём из аргумента или ставим дефолтное
MESSAGE=${1:-"chore: auto deploy $(date '+%Y-%m-%d %H:%M')"}

git add -A
git commit -m "$MESSAGE"
git push origin master

echo "🚀 Задеплоено: $MESSAGE"

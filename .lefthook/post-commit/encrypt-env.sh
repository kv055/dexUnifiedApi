#!/usr/bin/env bash
set -e

export PATH="$HOME/.local/bin:$PATH"

LAST_MSG=$(git log -1 --pretty=%B HEAD)
if [[ "$LAST_MSG" == ".env values update"* ]]; then
  exit 0
fi

if [[ ! -f ".env" ]]; then
  exit 0
fi

REPO_NAME=$(basename "$(git rev-parse --show-toplevel)")

cp .env .env.enc
dotenvx encrypt -f .env.enc

git add .env.enc
git commit -m ".env values update $REPO_NAME"

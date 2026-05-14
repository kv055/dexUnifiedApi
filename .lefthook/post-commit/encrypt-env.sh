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

HASH_FILE=".git/env.hash"
CURRENT_HASH=$(sha256sum .env | awk '{print $1}')

if [[ -f "$HASH_FILE" ]]; then
  STORED_HASH=$(cat "$HASH_FILE")
  if [[ "$CURRENT_HASH" == "$STORED_HASH" ]]; then
    exit 0  # .env has not changed, skip encryption
  fi
fi

REPO_NAME=$(basename "$(git rev-parse --show-toplevel)")

cp .env .env.enc
dotenvx encrypt -f .env.enc

echo "$CURRENT_HASH" > "$HASH_FILE"

git add .env.enc
git commit -m ".env values update $REPO_NAME"


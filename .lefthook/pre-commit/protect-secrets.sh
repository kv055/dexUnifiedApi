#!/usr/bin/env bash
set -euo pipefail

readonly GITLEAKS_VERSION="8.30.1"
offending_paths=()

while IFS= read -r -d '' path; do
  basename=${path##*/}
  case "$basename" in
    .env|.env.*)
      case "$basename" in
        *.enc|*.example) ;;
        *) offending_paths+=("$path") ;;
      esac
      ;;
  esac
done < <(git diff --cached --name-only --diff-filter=AMR -z)

if ((${#offending_paths[@]})); then
  printf 'ERROR: refusing to commit plaintext dotenv file(s):\n' >&2
  printf '  %s\n' "${offending_paths[@]}" >&2
  printf '\nRemove them from the index, keep plaintext local, and commit the corresponding encrypted .env.enc instead.\n' >&2
  exit 1
fi

if ! command -v gitleaks >/dev/null 2>&1; then
  printf 'ERROR: gitleaks is required for commits. Install pinned version %s:\n' "$GITLEAKS_VERSION" >&2
  printf '  go install github.com/gitleaks/gitleaks/v8@v%s\n' "$GITLEAKS_VERSION" >&2
  exit 1
fi

gitleaks git --pre-commit --staged --redact --config .gitleaks.toml

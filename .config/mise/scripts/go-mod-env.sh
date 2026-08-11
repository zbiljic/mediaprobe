#!/usr/bin/env sh

if [ -n "${GO_MOD_COMPAT:-}" ]; then
  export GO_MOD_COMPAT
fi

if [ -z "${GO_MOD_COMPAT:-}" ] && [ -f go.mod ] && command -v go >/dev/null 2>&1; then
  GO_MOD_COMPAT="$(
    go mod edit -json 2>/dev/null \
      | awk -F'"' '/"Go":/ { print $4; exit }' \
      | tr -d '[:blank:]'
  )"
  export GO_MOD_COMPAT
fi

if [ -z "${GO_MOD_COMPAT:-}" ]; then
  echo "GO_MOD_COMPAT is empty; ensure go.mod contains a Go version" >&2
  exit 1
fi

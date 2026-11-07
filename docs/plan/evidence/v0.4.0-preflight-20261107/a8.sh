#!/bin/sh
set -eu
hint=${1:?absolute path to Hint}
cd "${2:?absolute path to the fresh primary fixture}"
unset SSH_AUTH_SOCK
# Run on macOS with a configured local profile.
{
  printf '%s\n' '/repos clone https://example.invalid/sample.git ./existing existing' \
    '/repos clone https://fixture:placeholder@example.invalid/sample.git ./creds creds' \
    '/repos clone https://example.invalid/sample.git?fixture=1 ./query query' \
    '/repos clone https://example.invalid/sample.git#fixture ./fragment fragment' \
    '/repos clone ../second-fixture ./local local' \
    '/repos clone ssh://git@127.0.0.1:1/sample.git ./partial partial' '/repos' '/exit'
  sleep 2
} | script -q /dev/null "$hint" --plain --provider local
for dir in existing creds query fragment local partial; do
  if test -d "$dir"; then printf '%s exists\n' "$dir"; else printf '%s absent\n' "$dir"; fi
done

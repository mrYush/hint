# Recorded SSE fixtures

Byte streams replayed by `client_test.go` through `httptest`. They mirror the
Chat Completions streaming dialect: one JSON chunk per `data:` line, records
separated by blank lines, `[DONE]` sentinel at the end.

To record a new fixture from a live endpoint:

    curl -sN https://<base>/chat/completions \
      -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
      -d '{"model":"...","stream":true,"stream_options":{"include_usage":true},"messages":[...]}' \
      > new_fixture.sse

Strip anything sensitive (keys never appear in bodies, but ids may) before
committing.

## Live tests

`go test -tags live ./internal/provider/...` runs the optional live matrix
(streaming text + one tool-call round trip). It expects:

- `API_BAR_KEY` — api-bar.ru gateway profile
- `OPENAI_API_KEY` — api.openai.com
- Ollama listening on `localhost:11434` with a small model pulled

The default `go test ./...` never touches the network; the CI job for the
live matrix waits for WP0.10.

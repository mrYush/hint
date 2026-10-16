# hint RPC

`hint serve` and `hint --mode rpc` speak newline-delimited JSON-RPC 2.0.
One JSON object per line.
The methods below are protocol version 1.

`hint serve` listens on a Unix socket (`$XDG_RUNTIME_DIR/hint/hint.sock`, or `~/.local/state/hint/hint.sock`) and writes `auth.token` next to it, mode 0600.
On Windows the listener is `tcp 127.0.0.1:<port>`.
TCP on any other address is refused.
The address actually bound is written to `listen` in the same directory.

`hint --mode rpc` uses stdin and stdout.
It does not use a token: the process that holds the pipes is the client.

The CLI itself is a client of this protocol.
Bare `hint` and `hint -p` run the core in the same process and talk to it over an in-process pipe with exactly these messages; `hint --connect` talks to a running `hint serve` instead, reading `listen` and `auth.token` from the runtime directory (or `--connect unix:/path`, `--connect tcp:127.0.0.1:port`).
The Go client is `internal/rpc.Client`.

## Handshake

The first request must be `hello`.
Any other method before it is rejected.

```json
{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocol_version":1,"token":"<auth.token>"}}
```

`token` is required for `hint serve` and omitted for `--mode rpc`.
`protocol_version` must be 1; there is no fallback to an older dialect.

```json
{"jsonrpc":"2.0","id":1,"result":{"protocol_version":1,"server":"hint","version":"<build>"}}
```

## Sessions

The server is bound to the working directory it was started in.
Tools cannot leave that directory.

`session.open` creates a session.
With `{"ephemeral":true}` it is not written to disk: it has an id for the life of the server and nothing else (`hint --no-session`).
`session.attach` with `{"id":"..."}` continues one that already exists (an id prefix is enough when it is unique).
Both return the session:

```json
{"jsonrpc":"2.0","id":2,"result":{"id":"<id>","created":"2026-10-12T06:48:19Z","messages":[],"warnings":["..."]}}
```

`messages` is the conversation so far, as `pkg/agentapi` messages; `warnings` is what loading the file had to repair, usually absent.
`session.list` returns `{id, cwd, messages, created, modified, first_prompt}` rows, most recently modified first; `messages` there is a count.

A session has at most one turn in flight.
The connection that called `turn.start` owns that turn: only it may `permission.reply` and `turn.cancel`.
Another connection may attach, and may start the next turn once `turn.done` has arrived.

## Turns

```json
{"jsonrpc":"2.0","id":3,"method":"turn.start","params":{"session_id":"<id>","text":"what does main.go do?"}}
```

The response is sent before the turn finishes, so the client can cancel and answer permissions while events are still arriving:

```json
{"jsonrpc":"2.0","id":3,"result":{"turn_id":"<id>"}}
```

Events are notifications.
The payload is a `pkg/agentapi` event (`turn_start`, `text_delta`, `thinking_delta`, `message`, `permission`, `tool_start`, `tool_end`, `compaction`, `usage`, `error`, `turn_end`):

```json
{"jsonrpc":"2.0","method":"event","params":{"turn_id":"<id>","session_id":"<id>","event":{"kind":"text_delta","text":"..."}}}
```

The turn ends with:

```json
{"jsonrpc":"2.0","method":"turn.done","params":{"turn_id":"<id>","session_id":"<id>","finish_reason":"stop"}}
```

`turn.cancel` with `{"turn_id":"<id>"}` cancels the context.
The turn ends with `finish_reason` `canceled`.

## Permissions

A `permission` event is the question.
The turn blocks until the owning connection answers, the turn is cancelled, or 2 minutes pass.
Silence denies.

```json
{"jsonrpc":"2.0","id":4,"method":"permission.reply","params":{"turn_id":"<id>","call_id":"<call>","decision":"allow"}}
```

`decision` is `allow`, `deny`, or `always`.
`always` remembers the grant for the rest of that session only.
It is offered only when the request carries `always`, a label of what would be remembered (`commands starting with "go test"`); the core decides that, the client only shows it.
An `always` answer to a request without the label is treated as `allow`.

## Shutdown

`shutdown` stops the server.
On a socket server, closing the listener also ends `hint serve`.
On stdio, closing the pipe ends the read.

## Errors

Standard JSON-RPC codes (`-32700` parse, `-32600` invalid request, `-32601` method not found, `-32602` invalid params, `-32603` internal).
hint adds `-32001` unauthorized, `-32002` unsupported protocol version, `-32003` hello required, `-32004` session busy, `-32005` not found.

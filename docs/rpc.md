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
Bare `hint` and `hint -p` run the core in the same process and talk to it over an in-process pipe with exactly these messages; `hint --connect` talks to a running `hint serve` instead, reading `listen` and `auth.token` from the runtime directory (or `--connect unix:/path`, `--connect tcp:localhost:port`).
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

The server's startup directory is its immutable primary root.
File tools use that root by default and can select other roots explicitly attached to the session through workspace administration.
Shell commands retain the existing execute policy and are not an operating-system filesystem sandbox.

`session.open` creates a session.
With `{"ephemeral":true}` it is not written to disk: it has an id for the life of the server and nothing else (`hint --no-session`).
Both shells' `/new` is this method: a recorded session, or an ephemeral one when the run itself is `--no-session`.
It answers with the default profile.
`session.attach` with `{"id":"..."}` continues one that already exists (an id prefix is enough when it is unique).
Both return the session:

```json
{"jsonrpc":"2.0","id":2,"result":{"id":"<id>","created":"2026-10-12T06:48:19Z","messages":[],"warnings":["..."]}}
```

`messages` is the conversation so far, as `pkg/agentapi` messages; `warnings` is what loading the file had to repair, usually absent.
`profile` and `model` say what the session answers with (see below); `parent` is present on a fork: `{"id": "<source>", "turn": N}`.
`session.list` returns `{id, cwd, messages, created, modified, first_prompt, parent}` rows, most recently modified first; `messages` there is a count and `parent` is set on forks, so a client can draw the tree.

### Workspace roots

`session.repos` administers an open session's explicitly attached roots and returns bounded text plus root metadata where applicable.
It is an optional protocol-v1 extension; an older core returns `-32601 method not found`.

```json
{"jsonrpc":"2.0","id":20,"method":"session.repos","params":{"session_id":"<id>","operation_id":"repos-1","action":"add","path":"../library","alias":"library"}}
```

Actions are `list`, `add`, `remove`, `clone`, `tree` and `index`.
`add` takes `path` and an optional `alias`; `clone` additionally requires `url` and an absent destination.
`remove` takes `repo`, a root ID or alias.
`tree` accepts `repo` and a root-relative `path`, or just a continuation `cursor`; `list` accepts its own continuation cursor.
`index` reads diagnostics or changes `mode` to `off`, `auto` or `on` for the Hint process.
Root membership mutations require an idle affected session; an index mode change requires all turns to be idle.
The result carries `text` and, for root listings, `roots`, `revision` and an optional continuation cursor.
Use the returned text for explicit partial/paused/capacity status rather than inferring completeness from an empty page.

```json
{"jsonrpc":"2.0","id":21,"method":"session.repos.cancel","params":{"operation_id":"repos-1"}}
```

Operations execute independently so cancellation and turn permissions remain responsive during a clone.
Cancellation is scoped to that connection's operation ID.
The [workspace guide](workspaces.md) describes the corresponding `/repos` commands, instruction preparation, experimental indexing and resource limits.

### Profiles

A session answers with one provider profile at a time: the configured default until it is switched.

```json
{"jsonrpc":"2.0","id":5,"method":"profile.list"}
```

```json
{"jsonrpc":"2.0","id":5,"result":{"profiles":[{"name":"vllm","kind":"openai","model":"Qwen/Qwen2.5-Coder-32B-Instruct","default":true,"usable":true},{"name":"local","kind":"ollama","model":"qwen2.5:7b","fallback":true,"usable":true},{"name":"cloud","kind":"openai","usable":false,"problem":"selected profile \"cloud\" needs an API key: ..."}]}}
```

The list is read from the configuration files as they stand now, not as they were when the server started.

```json
{"jsonrpc":"2.0","id":6,"method":"session.profile","params":{"session_id":"<id>","profile":"local","model":"qwen2.5:14b"}}
```

`model` is optional and overrides the profile's own for this session.
The core re-reads the configuration first, so a key written by `hint setup` since the start is in force without a restart.
The result is what is in effect, the model filled in from the profile when it was not given:

```json
{"jsonrpc":"2.0","id":6,"result":{"profile":"local","model":"qwen2.5:14b"}}
```

The switch is recorded in the session file, so a later `session.attach` answers with the same profile; a profile the configuration no longer has, or that cannot be used, falls back to the default and says so in `warnings`.
An unknown profile is `-32005 not found`; one that exists but cannot be used (an openai profile without a key, an ollama profile without a model) is `-32602 invalid params` with the reason; a session with a turn in flight is `-32004 busy` — switch after `turn.done`.

### Forks

```json
{"jsonrpc":"2.0","id":7,"method":"session.fork","params":{"id":"<source>","turn":3}}
```

`session.fork` starts a new session whose conversation is the source's through the end of user turn `turn`, counted from 1: everything before the question that opens turn `turn + 1`.
A turn, not a message index: a client counts the questions it showed, but not the tool messages the core added between them.
`turn` omitted or 0 means the whole conversation.
`{"ephemeral": true}` keeps the fork off the disk, as for `session.open`.
The fork answers with the source's profile, is open on the server like a session just opened, and its file names the source in its header; the result is the same view `session.open` returns, with `parent` set.
A source with a turn in flight is `-32004 busy`; a turn the source does not have, or an empty source, is `-32602 invalid params`; an unknown source is `-32005 not found`.

### Completion

`complete` asks a model for a short continuation of what the user is typing.
It is a plain chat request without tools, to the session's profile and model unless the configuration's `completion.profile` names another; nothing of it reaches the session file.

```json
{"jsonrpc":"2.0","id":8,"method":"complete","params":{"id":"c7","session_id":"<id>","text":"add a test for"}}
```

`id` is the client's and is echoed back, so a late answer for text that is no longer in the input is told from the one being waited for.
`session_id` is optional; with it the session's last question goes to the model as context.
The request runs beside the connection's other requests: `permission.reply` and `turn.cancel` are not held up while a model is thinking.
One completion at a time per connection — a newer `complete` cancels the older, whose client no longer wants its answer.

```json
{"jsonrpc":"2.0","id":8,"result":{"id":"c7","suggestion":" the parser","usage":{"input_tokens":212,"output_tokens":4}}}
```

`suggestion` is empty when the model had nothing; `usage` is the provider's accounting, for a client that shows what completion costs.
`completion.mode` is when a client asks, not whether the core answers: a shell that turned completion on for a session (`/completion tab`) is answered while the configuration says `off`.
When `completion.profile` names a profile that cannot be used, the result is `{"id":"c7","disabled":true,"reason":"..."}`: a client then stops asking.
`complete.cancel` with `{"id":"c7"}` stops the provider call; cancelling a completion that already answered is fine.
A cancelled completion is answered with an error.
`complete.settings` returns the completion block as the files stand: `{"mode":"tab","profile":"","model":"","idle_after_ms":600,"warn_after_ms":2000}`.

`profile.models` with `{"profile":"local"}` returns `{"models":["qwen2.5:7b", ...]}`, what the profile's endpoint serves; an unknown profile is `-32005 not found`, an endpoint that does not answer `-32603`.
Like `complete`, it runs beside the connection's other requests, so its answer may come after those of requests sent later.

`session.profile`, `session.fork`, `profile.list` (`v0.3`, WP1.5) and `complete`, `complete.cancel`, `complete.settings`, `profile.models` (WP1.6) were added without a protocol version bump: an older core answers them with `-32601 method not found`, which a client treats as "not supported".

### MCP prompts

`prompt.servers` returns `{"servers":["docs"]}` from the enabled configuration without connecting to those servers.
The TUI uses it to decide whether a command prefix needs prompt discovery; an unknown slash-command prefix does not start MCP servers.

`prompt.list` returns `{"prompts":[{"server":"docs","name":"review","description":"Review a file.","arguments":[{"name":"path","required":true}]}]}`.
It connects to configured MCP servers on demand; a server without prompts contributes an empty list.

`prompt.get` expands a prompt with positional string arguments in the order declared by `arguments`:

```json
{"jsonrpc":"2.0","id":9,"method":"prompt.get","params":{"server":"docs","name":"review","arguments":["main.go"]}}
```

The result is `{"text":"..."}`.
A shell sends that text as a user turn, never as system text or a recorded assistant answer.

`prompt.complete` accepts `{"server":"docs","prompt":"review","argument":"path","value":"ma","arguments":{"repo":"hint"}}` and returns `{"values":["main.go", ...]}` from MCP `completion/complete`.
The optional `arguments` object carries already resolved arguments as MCP completion context.
A server without completion support returns an empty `values` list.
`has_more` is true when the server had further values it did not return.
Missing `server`, `name`, `prompt` or `argument` fields produce `-32602 invalid params`; an unknown server or prompt produces `-32005 not found`.
`prompt.list`, `prompt.get` and `prompt.complete` run beside the connection's other requests, like `complete` and `profile.models`, so `permission.reply` and `turn.cancel` are not delayed by an MCP call.
Stopping the client does not cancel `prompt.get` or `prompt.complete` on the core; that work ends when the connection closes or the MCP timeout fires.
These four methods were added by WP2.9 without a protocol version bump; an older core answers `-32601 method not found`.

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

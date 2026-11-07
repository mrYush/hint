# Working with multiple roots

WP2.2 adds explicit workspace roots to the existing session and permission model.
The startup working directory is the primary root and remains the default when an agent tool omits `repo`.
Git repositories, worktrees and ordinary local folders can be attached to the same conversation.

## Attach and navigate

Both the TUI and `hint --plain` accept these commands:

```text
/repos
/repos add ../library library
/repos add "/path/to/working documents" documents
/repos clone https://github.com/example/project.git ../project project
/repos tree library
/repos tree library src
/repos next <tree-cursor>
/repos list <root-list-cursor>
/repos remove library
```

Relative local paths resolve from Hint's startup directory.
Only user actions add roots; a repository file or an agent's note cannot attach a parent directory or another repository.
Root additions and removals require an idle session and are recorded before membership changes become active.
Resume restores that session's membership; a fork uses membership at the selected turn, including earlier detached-root boundaries.
Unavailable restored roots are reported and remain inactive.
The primary root cannot be removed.
The registry admits at most 256 root identities, including retained ownership reservations; this bounds session metadata independently of file counts within each root.

Root lists and trees are paginated text.
Tree pages describe current catalog observations and may be incomplete while discovery is paused or still running.
An empty observed page does not prove the directory is empty.
Continue using the cursor printed by the relevant command; a membership change invalidates old cursors.
Opaque entry IDs preserve navigation and reads when a display path is abbreviated or cannot be represented faithfully in JSON.

Nested repositories, submodules and explicitly attached nested ordinary folders have separate ownership boundaries.
The parent can show the boundary but cannot read or modify the child's contents through its own identity.
Historical boundary markers are explicitly labeled as ownership reservations with unverified availability; they do not assert that a detached or offline directory still exists.
Attach the child explicitly to work there; cloning does not initialize submodules.
A clone requires a new destination and is attached only after success.
On failure, a newly created partial destination stays unattached so the user can inspect it.
HTTPS URLs with credentials, local clone paths and URL query/fragment fields are rejected.

## Code-map capability and resources

```text
/repos index
/repos index on
/repos index off
/repos index auto
```

The setting applies to the current Hint process, including its sessions, and resets to `auto` after restart.
Changing it requires that no turn is running; status remains readable during a turn.
The next turn receives the corresponding tool catalog and a fresh context-cost baseline.

| Mode | Behavior in this implementation |
|---|---|
| `auto` | Conservative default: automatic indexing and `repo_map` disclosure await a measured resource/model profile; status explains `resource_profile_unvalidated`. |
| `on` | Explicit experimental opt-in to `repo_map` and eligible background work under finite budgets and current resource checks. |
| `off` | No `repo_map` disclosure or extraction; ordinary scoped file tools and workspace administration remain available. |

No mode grants additional filesystem access or bypasses memory/disk emergencies.
Background work pauses while Hint has an active model request, including completion, compaction and fallback.
Unknown or stale resource observations pause background work rather than being treated as spare capacity.
Known memory/disk emergencies also stop focused extraction while retaining bounded catalog reads; an unknown sample cannot clear an already observed emergency.
Linux and macOS have local resource samplers; unsupported Windows background telemetry remains unavailable, while bounded explicit navigation and extraction are still portable.
Indexing does not emit unsolicited progress messages; `/repos index` reports its effective state and reason.

The experimental resource envelope is a configuration for evaluation, not a measured performance guarantee.
The user's Pi 4/local-model measurements and 8k/32k usefulness evaluation remain pending under the [manual methodology](plan/repo-map-evaluation.md).
An automatically paused run is not an indexing-on benchmark.

The catalog is local, outside source roots, under the operating system's user cache directory at `hint/index/code-v2.sqlite`.
The default storage allowance is 128 MiB, including reserved rollback-journal headroom; only approximately one third is available for database pages.
For a larger tree, choose a larger finite allowance when starting the core:

```sh
hint --index-max-bytes 1073741824
hint serve --index-max-bytes 1073741824
```

The flag is startup configuration, not a setting a repository can supply to itself.
The shared catalog remembers its disk allowance; all Hint processes using it must request the same value.
To change it, close every Hint process using that cache, move the derived `code-v2.sqlite` cache aside, and restart with the new allowance; Hint rebuilds its observations.
Do not remove or move a live SQLite database or its journal.
An incompatible allowance or an initialization failure disables the optional code index with a diagnostic; ordinary Hint operation remains available.
Reaching a quota produces explicit partial/unavailable status rather than silently evicting a live pagination reservation.
Background work waits at least five seconds between capacity retries and one second after SQLite contention, retaining unfinished tasks.
It resumes when completed work or version cleanup makes space available; a fully occupied catalog without reclaimable data remains partial.
Unreadable directories remain queued for delayed retry and do not prove that their contents were deleted.
Large trees have bounded active work and memory; complete indexing still requires enough disk space, idle time and a sufficiently stable filesystem.
Directory discovery retains at most one active stream per root and eight across the process, so one large root cannot occupy every stream.
With more than eight roots waiting for directory streams, additional roots report `directory_stream_capacity` until a slot becomes available.
Live filesystem walks use bounded directory batches and finite examination/depth/path budgets; a traversal that reaches them asks for a narrower scope rather than reporting complete results.
The startup instruction loader also caps rule discovery and aggregate input, reporting omissions rather than reading an arbitrarily large rules directory into memory.
The current SQLite schema v5 migrates v2/v3/v4 caches transactionally and rejects the incompatible experimental v1 schema.
The cache filename stays `code-v2.sqlite`; its name identifies the entry-ID format, not the latest internal schema.
Close older Hint binaries before allowing a newer binary to upgrade the shared cache.

## Agent workflow

`repo_list` exposes attached roots and observed tree pages; `{"status":true}` requests the bounded resource/capability diagnostics.
`repo_map` requests declarations and signatures in one selected root, with optional subtree and exact/prefix identifier filters.
Its Go, Python, JavaScript, TypeScript/TSX and Rust adapters return source locations without function bodies.
Unsupported or oversized files remain navigation entries with explicit omissions.
Root-level coverage includes finite reason/count buckets; legacy omissions remain `unclassified` until their source records are updated.
Subtree queries omit unknown aggregate counts, and small responses may omit optional coverage fields together.
The map is a tool result, not an automatic addition to the system prompt.

Before a secondary-root edit or command, the agent calls `repo_prepare` for that root and target in a separate model round.
Hint reads that scope's supported instruction files and rules through rooted access and adds a bounded trusted instruction snapshot to the next request.
Only one secondary target is prepared at a time.
An edit in the same tool batch as preparation is refused; changed instructions require preparation again.
The `instructions` tool accepts `repo` to read full documents from the prepared scope.
Existing primary-root instruction behavior remains in effect.

The file tools accept `repo`; file reads/writes/edits can also use a scoped `entry_id` instead of a path.
Every use rechecks authority, nested boundaries and configured exclusions, and existing write/execute permission rules still apply.
Shell commands run with the selected working directory under the existing execute policy; they are not an operating-system filesystem sandbox.

Map pages contain a retained generation, scope, coverage, source versions and freshness.
`content_checked` records a complete current-request reread/hash observation; `observed` records older extraction evidence that passed current mandatory access/stat checks.
Neither label replaces a current source read before editing.
Continue only with `cursor`; `query_complete: false` may accompany an empty page when the bounded scan made progress through filtered or invalidated records.
A complete query exhausts the retained generation and releases its reservation, so previously used tokens for that query are no longer reusable.
A missing or replaced attached root returns `root_unavailable`; a missing file in an available root returns `scope_not_found`.
Definitive root loss invalidates retained cursors; transient I/O errors allow retrying the same cursor.
Integrations configuring tool output below 1024 bytes omit `repo_map` from the catalog and explain the limit in `repo_list` diagnostics/description.
The [contract](plan/repo-map-contract.md) specifies exact output limits and error/freshness semantics.

Document full-text search, PDF/DOCX/OCR modules, durable navigation notes, embeddings and reference-graph ranking remain later v0.4 work; this implementation does not claim those capabilities.

# Phase 7 — Feature store (third-party catalog)

> Status: Planned · Target release: **v0.8–v1.0** · Estimate: 10–14 weeks (part-time)
> Depends on: Phase 2 (MCP), Phase 1 (stable agentapi)

## Goal

The public tool/skill library from the original product vision: third-party
developers package, publish, and distribute extensions; users discover and
install them safely.

## Work packages

### WP7.1 — Package formats

- [ ] **MCP server** (any language) — primary format
- [ ] **Declarative skill**: folder with manifest + prompt + resources (Claude Code skills model)
- [ ] **Go plugin** compiled to a separate binary (spawned, not `plugin.so`)
- [ ] Manifest schema: name, version, entry point, declared permission classes, platforms

### WP7.2 — Installation and updates

Reference — Pi packages.

- [ ] `hint install npm:<pkg> | git:<repo>@<tag> | https://…`
- [ ] Versions pinned in a lockfile; `hint update` upgrades explicitly
- [ ] `hint list` / `hint uninstall`
- [ ] Project-local vs. global installs

### WP7.3 — Trust model

Reference — Pi trust.json.

- [ ] Project-scoped packages require explicit directory trust
- [ ] Release signatures verified on install
- [ ] Manifest-declared permission classes shown to the user **before** install
- [ ] Sandbox by default for installed tools; MCP output remains untrusted input
- [ ] Revocation path: a package can be flagged and disabled remotely (opt-in check)

### WP7.4 — Catalog

- [ ] Stage 1: static registry — a git repository with an index (early-Homebrew model)
- [ ] CI validation of submitted manifests (schema, signature, permission sanity)
- [ ] Stage 2: website with search and ratings
- [ ] Moderation/review policy documented

### WP7.5 — SDK and templates

- [ ] Publish `pkg/agentapi` as a stable, semver-ed module
- [ ] `hint-tool-template` repositories (Go and TypeScript)
- [ ] Developer docs: how to build, test, and publish an extension

## Acceptance criteria

1. A third-party developer goes from template to installed, working tool in
   under 30 minutes using only public docs.
2. Installing a package shows its requested permissions; the agent cannot use
   an undeclared class.
3. A tampered package (bad signature) refuses to install.
4. The registry index is browsable and searchable (`hint search <term>`).

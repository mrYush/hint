# Phase 4 — Desktop widgets (macOS / Windows / Linux)

> Status: Planned · Target release: **v0.5** · Estimate: 6–10 weeks (part-time)
> Depends on: Phase 1 (RPC core), Phase 3 (camera/mic/screenshot scenarios)

## Goal

The "widget on the desktop" from the original product vision: thin native
shells over `hint serve`, with the core installed as a background service — a
resident assistant daemon.

## Work packages

### WP4.1 — Core as a system service

- [ ] Install/uninstall commands: `hint service install` → launchd (macOS), systemd user unit (Linux), autostart (Windows)
- [ ] Single-instance socket discovery for clients
- [ ] Idle resource budget: near-zero CPU, bounded RSS when no session is active
- [ ] Auto-update channel decision (goreleaser + built-in updater vs. package managers) — spike

### WP4.2 — macOS menu-bar app

- [ ] Swift/SwiftUI menu-bar shell talking to the core over unix socket
- [ ] Global hotkey → quick-ask window
- [ ] Screenshot-to-question flow
- [ ] Camera/mic capture via native APIs, streamed to the core (STT/VLM)
- [ ] Permission prompts rendered natively (reuse the RPC `permission_request` event)
- [ ] Signed + notarized build

### WP4.3 — Windows tray app

- [ ] Spike: WinUI 3 vs. Wails (decision Q3 in PLAN.md) — pick one, record the decision here
- [ ] Tray shell with the same features as WP4.2 (hotkey, quick ask, screenshot, mic)
- [ ] Installer (MSIX or Inno Setup)

### WP4.4 — Linux tray app

- [ ] Tray via Wails/GTK, same feature set
- [ ] Wayland + X11 screenshot paths

## Acceptance criteria

1. After `hint service install`, the core survives reboot and the widget
   connects instantly.
2. Global hotkey → question → answer without opening a terminal, on all three
   OSes.
3. A screenshot taken from the widget produces a correct VLM answer.
4. Killing the widget does not kill in-flight agent work (core owns the turn).

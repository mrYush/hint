# Phase 6 — Sensors and devices

> Status: Planned · Target release: **v0.7** · Estimate: 8–12 weeks (part-time)
> Depends on: Phase 2 (MCP client) · Can run in parallel with: Phase 5

## Goal

Raspberry Pi as a "home agent with sensors", the phone as an "agent with
perception": device sensors become first-class context sources, delivered
through the same MCP mechanism as any other tool.

## Design

Sensors are packaged as **MCP servers running inside the client**: the
mobile/desktop shell hosts a local MCP server exposing tools like
`get_location`, `read_sensor`, `capture_photo` — the core consumes them via
the ordinary MCP client from Phase 2 (approach reference — Goose: everything
extends through MCP).

## Work packages

### WP6.1 — SensorSource abstraction and client-side MCP

- [ ] `SensorSource` contract: identity, permission class, sampling policy, freshness
- [ ] Client-hosted MCP server skeleton (one per shell platform), registered with the core automatically
- [ ] Permission model: every sensor read is user-visible and per-sensor grantable

### WP6.2 — Desktop/mobile sensors

- [ ] Geolocation, motion/pedometer (mobile), network state, battery, calendar, clipboard
- [ ] Per-platform capability table documented (what exists where)
- [ ] Sensor values injected as context only on demand (tool call), never silently

### WP6.3 — Raspberry Pi: GPIO/I2C

- [ ] GPIO/I2C sensor MCP server (temperature, humidity, motion, etc.)
- [ ] Wiring/config docs for common HATs
- [ ] Example: "tell me when the room gets cold" end-to-end

### WP6.4 — Proactive scenarios

- [ ] Subscriptions to sensor events → trigger an agent turn (opt-in)
- [ ] Trigger rules live in config with explicit permission grants
- [ ] Rate limiting and quiet hours
- [ ] Every proactive turn logged to a session like a normal one

## Acceptance criteria

1. On a phone, "where am I and what's around?" resolves via the location tool
   with a visible permission grant.
2. A Raspberry Pi with a temperature sensor answers "what's the temperature?"
   and can proactively alert on a threshold (opt-in rule).
3. Disabling a sensor permission immediately stops the corresponding tool.

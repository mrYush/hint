// Package rpc is the Phase 1 core-as-a-service transport: newline-delimited
// JSON-RPC 2.0 over a Unix socket, a loopback TCP port, or stdio.
//
// The agent itself stays behind [Engine]. A connection authenticates with
// hello, then opens or attaches a session and runs turns. Events travel as
// notifications while the turn runs; a permission question blocks inside
// the engine until the same connection answers it or the wait expires,
// which denies.
//
// The wire format is documented in docs/rpc.md.
package rpc

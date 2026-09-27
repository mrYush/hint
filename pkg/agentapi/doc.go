// Package agentapi defines the public contract of the hint agent: the wire
// types exchanged between the agent core, providers, tools, and clients, and
// the interfaces those components implement.
//
// Everything exported from this package is SDK surface. Third parties are
// expected to write providers and tools against it, sessions are persisted in
// terms of it, and the Phase 1 core-as-a-service RPC transports it verbatim.
// Breaking changes here are exceptional and require a decision record under
// docs/plan/.
//
// # Layers
//
// The package covers three layers that deliberately do not depend on each
// other:
//
//   - Conversation state: [Message], [ContentPart], [ToolCall], [ToolResult].
//     This is what a provider is handed and what a session file stores.
//   - Provider streaming: [ChatProvider] and the [ChatEvent] stream it
//     produces, plus the not-yet-implemented modality interfaces
//     ([Embedder], [Transcriber], [Speaker], [VisionProvider]).
//   - Agent observation: [Event], the stream a client (CLI, TUI, widget,
//     mobile shell) consumes to render a turn.
//
// # Wire format rules
//
// Every type in this package round-trips losslessly through encoding/json:
// marshalling a value and unmarshalling the result yields an equal value.
// Sum types are modelled as a struct with a Kind discriminator rather than as
// a Go interface, so no custom (Un)MarshalJSON is needed anywhere. Kind and
// payload can disagree in a hand-written value, so such types carry a
// Validate method; decoders of untrusted input are expected to call it.
//
// JSON field names are snake_case. Optional fields are omitempty so that the
// on-disk session format stays readable and diffable.
//
// # Metadata
//
// Wire types carry no identity or timestamps: a [Message] holds only what can
// be sent to a provider. Identifiers, timestamps, and per-record bookkeeping
// belong to the session record that wraps a message.
package agentapi

// WireVersion is the revision of the types in this package. It is written
// into persisted artifacts (session headers) so that a reader can recognise a
// format it does not understand instead of silently misreading it.
//
// Bump it whenever an existing field changes meaning, is removed, or is
// renamed. Adding an optional field or a new Kind constant does not require a
// bump: decoders ignore unknown fields and are expected to treat unknown Kind
// values as skippable.
const WireVersion = 1

# Subscription-gated model access

> Status: **Design spike — nothing here is implemented.** Not scheduled before
> Phase 7 (WP7.6). Tracked as Q9 in [`../../PLAN.md`](../../PLAN.md).
> Last updated: 2026-09-08.
>
> This document exists so that the provider layer does not foreclose the
> design by accident, and so the hard question is answered on paper before
> any code assumes an answer.

## The problem

Suppose access to some models is sold as a subscription rather than paid per
token by the user — the shape Perplexity uses for Portable Computer's cloud
advisor, and the shape every "agent CLI with a plan attached" ends up in.
Then the client has to carry a credential that unlocks a model the user has
not paid a provider for directly.

That credential lives on a machine the user administers, in a client whose
source is public and which can be rebuilt with any modification. The question
is not *how do we hide it* — it is:

> **How do we make the credential worthless for anything except the product
> it was issued for?**

Everything below follows from taking that question literally.

## What cannot work, and why

These are ruled out now so that they are not re-proposed later under time
pressure. Each fails against the same adversary: someone who controls the
machine the client runs on.

1. **A provider key shipped in the binary or written into config.** It is
   readable with `strings`, with a debugger, or from process memory.
   Obfuscation buys hours against a motivated reader and nothing against a
   bored one.
2. **Client attestation** — "prove that you are an unmodified `hint`". This
   requires a hardware root of trust the *user* does not control. Mobile
   platforms offer one (App Attest, Play Integrity); the desktop, the server
   and the Raspberry Pi do not — and those are the platforms the product is
   defined by (C1 in [`why-hint.md`](why-hint.md)). An attestation scheme
   that only works on the two platforms we ship last is not a scheme.
3. **A "secret" header, custom framing, or TLS pinning as a security
   control.** The user terminates their own TLS with a local proxy whenever
   they want. Pinning is an availability and misconfiguration control, not an
   authentication one.
4. **A passthrough chat endpoint with a server-pinned system prompt.** This
   is the one that looks reasonable. It is not: the client still supplies the
   user turn and the tool definitions, so the caller can put an arbitrary
   workload in the request. It constrains the *voice* of the answer, not the
   work being bought.

**The correct move is not to protect the credential.** Assume it will become
public, and make the thing it unlocks too narrow to be worth stealing.

## The design, in layers

Each layer is independently useful and can ship without the ones below it.

### L0 — The client never holds a provider credential

The subscription talks to a gateway we run; the gateway holds the upstream
provider keys server-side. What ships to the user is a *subscription token*,
which is not accepted by `api.openai.com` or any other upstream — it is not a
provider key at all. This already answers the literal question for the
upstream API. What it leaves open is whether the token is useful for
arbitrary work *against our own gateway*, which is L1.

### L1 — The gateway is a product API, not a passthrough

This is the decision that actually settles the question. Three options:

| Option | Shape | A stolen token buys | Verdict |
|---|---|---|---|
| **A. Transparent proxy** | `POST /v1/chat/completions`, OpenAI dialect | a general-purpose LLM endpoint — any prompt, any workload | **No.** Quotas and rate limits become the only defence, i.e. damage control after the fact. It also makes the token strictly more valuable than the subscription. |
| **B. Task-shaped API** | `POST /v1/advise {question, budget}` → text guidance; the gateway builds the prompt, the client cannot supply a system message, a tool list or a message history | exactly that one operation, at the quota of the account it was stolen from | **Recommended.** The token cannot be pointed at an unrelated workload because there is no endpoint that accepts one. |
| **C. Proxy + pinned preamble** | as A, with a server-side system prompt | very nearly what A buys (see "cannot work" #4) | **No** — the appearance of a control without the substance of one. |

Option B has a consequence that has to be accepted openly: **you cannot run
the agent loop over it.** Streaming tool calls, arbitrary histories and
compaction all need a general chat endpoint. So the subscription buys the
*advisor* role — a bounded question-and-answer escalation — while the main
loop keeps running on the user's own key or on a local model.

That is the same split Portable Computer arrived at, for a different reason:
they narrowed the cloud call to keep private data on the device, we narrow it
to contain a credential. The two motivations produce the same API, which is
a good sign for the shape. It also leaves C3 in `why-hint.md` intact — the
subscription becomes one more rung of the ladder, never the only path.

### L2 — Token shape: device-bound, short-lived, sender-constrained

- **OAuth 2.0 Device Authorization Grant (RFC 8628)** for issuance. Chosen
  over a browser redirect because a headless Raspberry Pi over SSH is a
  first-class client and has no browser to redirect.
- **Short-lived access tokens** (minutes), `aud`-restricted to the gateway,
  with a refresh token in the OS keychain — and a documented, visible
  downgrade to a `0600` file where no keychain exists (again: the Pi).
- **Sender-constrained tokens: DPoP (RFC 9449)** or mTLS-bound tokens
  (RFC 8705). The client generates a keypair per device; the access token is
  bound to the public key and every request carries a signed proof. A token
  copied off disk without the private key is inert. This is the strongest
  guarantee obtainable *without* attestation, and it is the answer to
  "someone read my config file" — which is the realistic threat, far more
  than "someone recompiled the client".
- **Device registry and revocation:** a bound device is listable and
  killable — `hint auth devices`, `hint auth revoke <id>` — with a
  per-subscription device cap.

### L3 — Accounting, and what it is not

Per-subscription quota, per-device rate limits, request-shape anomaly
detection, server-side kill switch. Stated plainly: **this layer is damage
control, not prevention.** It bounds the loss from a leaked token; it does
not stop the first misuse. It is listed last on purpose, because it is the
layer that is easy to build and tempting to mistake for a design.

### L4 — The non-negotiable

A user's own provider key must always remain a complete path to every
feature. A product whose offline story is a headline claim (C3) cannot make
the subscription load-bearing. Concretely: no feature ships that *requires*
the gateway, and the advisor rung is always optional in the UI.

## What this means for code today

Nothing needs building now. Three constraints keep the option open:

1. **`ChatProvider` must not assume a static credential.** Today
   `internal/provider/openai.Client` reads `c.profile.APIKey` at request time
   and sets `Authorization` itself (`internal/provider/openai/client.go:139`).
   A refreshing token would technically work by injecting a
   `RoundTripper` through the existing `WithHTTPClient` and letting it
   overwrite the header — that is an accident, not a seam. When this lands,
   add an explicit credential/token-source option instead.
2. **The profile `Kind` is the natural carrier.** `config.Kind` already
   distinguishes dialects (`openai`, `ollama`); a future `hint-gateway` kind
   adds a profile type without a config-schema break, and the router's
   failover semantics (WP0.3) apply to it unchanged.
3. **Nothing may bake "credential == a string that lives in config"** into
   the config loader's contract or into `agentapi`. This is the only rule
   that costs anything to violate.

## Open sub-questions

| # | Question | Decide when |
|---|---|---|
| Q9a | Is running a paid gateway in scope for a single part-time developer at all — payments, an abuse desk, an SLA, and upstream provider terms on reselling access? | Only once there are users. It is a business decision, and the honest default is "no". |
| Q9b | A task-shaped API means the gateway owns the prompt shapes, so gateway and client versions couple. What is the compatibility policy when an old client meets a new gateway? | Before WP7.6 implementation |
| Q9c | Does the same identity infrastructure serve the Phase 7 registry (publisher accounts, signature revocation, WP7.3)? Building one identity system instead of two is most of the argument for doing this at all. | With WP7.3 design |
| Q9d | Is there a self-hosted gateway story (a team runs its own, with its own upstream keys)? That inverts the trust model and removes most of L3. | Before WP7.6 implementation |

## Acceptance for the spike

The spike is done — before any implementation — when there is:

1. A written decision on L1 (A, B or C) with the reasoning recorded here.
2. A threat model naming the adversary, the assets, and what is explicitly
   accepted as unpreventable.
3. A note on upstream provider terms for the models in question.
4. Confirmation that L4 holds for every feature planned through Phase 7.

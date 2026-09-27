# Why `hint` exists

> Status: living document · Last updated: 2026-09-08
> Companion to [`architecture.md`](architecture.md): that file says *how* the
> system is built, this one says *why it is worth building at all*.

## The question this document answers

Four projects in the [prior-art table](architecture.md#prior-art-does-an-existing-tool-already-implement-the-whole-mechanic)
already ship a large part of what this plan describes, and two of them
(FutureOS, Goose) ship the architectural centrepiece — one core, thin clients
on every platform. Perplexity's Portable Computer ships local-first execution
further than our own offline story goes. A reviewer is entitled to ask why
this is not a fork, a set of plugins, or a wasted year.

The answer below is written to be *checkable*, not persuasive: three claims
that must all hold for the project to be worth its cost, what the position
costs us, and the conditions under which the honest move is to stop and fold
the work into someone else's core. If a claim stops being true, that is a
planning event with a defined response — not a matter of taste.

## Non-goals, stated first

Half the case for a project is what it refuses to compete on. `hint` is not:

- **A better coding agent than Claude Code, Codex or Crush.** Phase 0 is a
  deliberate re-implementation of a solved problem. Its purpose is not
  differentiation; see [Why start with a coding agent](#why-start-with-a-solved-problem) below.
- **Novel in core-as-a-service.** FutureOS and Goose demonstrate the pattern
  in production. We adopt it; we do not claim it.
- **A source of better models.** Provider-agnostic means we inherit exactly
  what the provider gives us, including its ceiling.
- **An enterprise compliance product.** "Your documents never leave the
  building" is Portable Computer's market, and it is well served. Our privacy
  story is a consequence of running locally, not the pitch.
- **A managed service.** The default deployment is a binary the user owns,
  with the user's own provider credentials.

## The three claims

The project is justified if — and only if — all three hold at once. Any one
of them alone is already shipped by someone else.

### C1 — The hardware floor, not the architecture

The distinguishing property is not "one core, many shells". It is *how small
the smallest supported machine is*.

| Project | Smallest first-class deployment |
|---|---|
| Portable Computer | RTX with ≥24 GB VRAM, or a $4,699 DGX Spark |
| OpenHuman | a desktop, Tauri app |
| Goose | a desktop; `goose-ios` is a remote client tunnelling back to it |
| FutureOS | a desktop backend; mobile clients talk to it |
| `hint` (target) | a Raspberry Pi running the core itself, and a phone running it in-process via gomobile |

Everyone else's mobile and small-hardware story is *remote control of a
desktop*. That is a legitimate design, and it is a different product: it
stops working in a basement, on a plane, or on a device that is the only
computer present. A no-cgo Go core that cross-compiles to `linux/arm64` and
embeds into an app is the mechanism that makes the small end first-class, and
it is why the language choice is load-bearing rather than a preference.

Note the direction of the argument: this is not "Go is better than Rust".
Rust would serve C1 equally well. The claim is that *nobody is aiming at the
small end*, and the Go core is how we aim at it without a second runtime.

### C2 — Perception declared in the contract before it is implemented

`pkg/agentapi` declares the modality interfaces (vision, speech-to-text,
text-to-speech) in WP0.1 — a phase in which none of them are implemented and
the product is a text CLI. This looks like premature abstraction and is the
opposite: it is the only cheap moment to do it.

Adding a modality to a shipped core-as-a-service system means changing the
wire contract that every shell already depends on (R7 in
[`risks.md`](risks.md)). Declaring it up front costs a few interface
definitions; retrofitting it costs a migration across CLI, TUI, desktop
widget, iOS, Android and Pi at once. This is why the competitors that started
from a working product are text-and-tools shaped: Goose has no VLM tool,
Portable Computer handles text and files only, and OpenHuman — the one with
voice in the core — is desktop-first precisely because the voice path grew
into the app rather than through a contract.

The claim is therefore narrow and testable: *the phase-3 modalities land
without a `WireVersion` break*. If they cannot, C2 was wrong and the
contract-first cost bought nothing.

### C3 — Degradation is one continuous ladder, not two products

Portable Computer is the strongest evidence for and against this claim at the
same time. It proves that a fully local agent loop is viable and desirable —
and it ships as a *separate product* with its own hardware floor, its own
model pair and its own subscription, alongside the cloud Computer it does not
converge with.

Our bet is that the same binary should walk the whole ladder: frontier model
over the network when it is reachable and the task deserves it, Ollama on the
desktop, a small on-device model on a phone or a Pi, with the router (WP0.3)
choosing and failing over rather than the user maintaining two installs. The
[implications section](architecture.md#what-this-means-for-the-plan) adds two
policies to that ladder — data-minimising escalation and a preview of what
would leave the device — that only make sense if the ladder is continuous.

Testable form: a single `hint` config drives a run on a workstation and the
same run on a Raspberry Pi with no code change and no second product, and the
user can see which rung answered.

## Why start with a solved problem

Phase 0 rebuilds a coding CLI that a dozen mature tools already provide. That
is intentional, for three reasons that are worth writing down because the
choice looks like drift:

1. **It is the only harness with cheap ground truth.** A wrong edit is
   visible; a wrong answer about a camera frame is not. Correctness of the
   loop, the permission gate, sessions and compaction gets exercised hard
   before anything harder is built on it.
2. **It forces the contract to be real.** A contract with one implementation
   is a guess. `agentapi` has been through eleven work packages of load —
   tool scheduling, permissions, compaction checkpoints, instruction
   budgets — before a second shell exists.
3. **It is the phase where borrowing is legal and cheap.** The reference
   table in `architecture.md` is dense precisely here; the exotic phases have
   almost nothing to copy from.

The risk this creates is honest and recorded as R6 (scope drag): a project
can die in phase 0 by being a slightly worse Crush forever. The mitigation is
that every phase ships something usable, and that phase 0's *exit* criterion
is an acceptance run on a Raspberry Pi — hardware no competitor targets —
rather than feature parity with a coding agent.

## What the position costs

Stated plainly, because a rationale that lists only benefits is marketing:

- **No model/harness co-design.** Perplexity post-trained a model against
  their own harness and report roughly five points on their own benchmark
  for it. Provider-agnosticism forecloses that permanently. We are
  structurally slightly worse per unit of model, and we buy portability with
  it.
- **No Python ecosystem.** Every on-device runtime (whisper.cpp, llama.cpp,
  piper) is a subprocess or a cgo-gated dependency, not a library call. R1
  exists because of this choice.
- **The contract is the bottleneck.** Anything a shell wants must be
  expressible in `agentapi` first. This is the price of C2 and it will feel
  like friction in phases 4–6.
- **Six platforms, one part-time developer**, against funded teams shipping
  one platform well (R6).
- **No sandbox layer yet**, where Codex CLI, OpenHands and Portable Computer
  all have one — a real gap, not a design choice.

## Falsification: what would end this project

Each condition has a defined response, so that the answer is not decided in
the moment by sunk cost.

| If … | Then C… fails | Response |
|---|---|---|
| Goose (or any Apache/MIT core) ships an embeddable on-device runtime for phone and Pi, with sensors | C1 | Stop building a core. Become the sensor/GPIO MCP servers and the Go shells on top of theirs — the phase 6–7 work is portable to that world. |
| MCP absorbs modality and sensors into the spec, so a core is a thin dispatcher | C2 | Drop the custom modality interfaces, keep the router, the permission model and the small-hardware packaging. |
| A vendor ships a genuinely continuous ladder (phone → Pi → desktop, one install, open extensions) | C3 | The remaining differentiator is ownership and provider-agnosticism only. That may still be worth a CLI, but not eight phases — re-scope to phases 0–3. |
| Phase 3 cannot land without a `WireVersion` break | C2 | The contract-first premise was wrong; stop paying for it and design modalities per-shell. |
| Phase 0 has not exited by its own criteria and phase 1 keeps slipping | R6 | The project is a coding-agent rewrite in denial. Cut to phases 0–1 and keep it as a personal tool. |

Re-check this table on the same cadence as the prior-art scan.

## Who this is for

One developer with heterogeneous hardware — a workstation, a laptop, a
phone, and something small and headless — who wants the same assistant on all
of them, with their own keys and their own models, and who is willing to
trade a few benchmark points for not depending on a vendor's uptime, pricing
or hardware floor.

Explicitly *not* the enterprise buyer with a compliance requirement and a
budget for a DGX Spark. That buyer is well served today, and competing for
them would cost exactly the properties above.

# Adaptive tuning effect: making `hint` feel fine-tuned without training on the user

> Status: Design spike · Settles Q12 · No branch until Phase 2R finishes the research and the roadmap assigns a work package
> Last updated: 2026-11-07

## The effect

**Adaptive Tuning Effect (ATE)** is the measurable reduction in repeated instructions and corrections that a user experiences when `hint` adapts its behavior from their interactions, using local context and memory instead of changing model weights.
The Russian term is **эффект адаптивной настройки (ЭАН)**; the user-facing phrase is **«эффект дообучения»**.
The mechanism behind it is **In-Context Adaptation (ICA)**: preferences, rules and relevant past decisions are selected and placed in the prompt, not baked into a model.
ATE is a claim about observed behavior, not about weights.
A user should notice that they stop repeating the same correction, that the tool follows their conventions sooner, and that a new project needs less setup than the last one.
It must never be described as training on the user's data.

What ATE is not:

- It is not fine-tuning, LoRA or a model update; no weights change.
- It is not a hidden cloud profile; the store is local, inspectable and deletable.
- It is not a replacement for `HINT.md`, `.hint/rules` or explicit settings; it is a lower-friction layer beside them.
- It is not learning from the model's own output without a user signal; that is how a self-reinforcing loop starts.
- It is not a promise that the tool knows the user better than the user does; every inferred preference stays reviewable.

## Why this is the product bet

Agent CLIs are converging on the same table stakes: tool calling, permissions, sessions, MCP, multimodal input and a TUI.
A feature can be copied in a release cycle; accumulated, user-specific context cannot.
The durable difference is a tool that becomes cheaper to use every week because it remembers how this person and this repository work.
The privacy constraint is also an advantage: local-first adaptation is something a cloud-only competitor cannot copy without asking for the data.
The efficiency claim follows from the same mechanism: less repeated instruction means fewer tokens, fewer tool calls and fewer corrections per task.

## Terminology

| Term | What it emphasizes | Cost |
|---|---|---|
| **Adaptive Tuning Effect (ATE)** | adaptation without weight training; neutral and measurable | "tuning" still hints at training, so it needs the one-line definition |
| Continuous Tuning Effect (CTE) | ongoing adaptation | promises always-on learning; hard to defend when the user stops giving signals |
| Self-Tuning Effect (STE) | autonomy | implies the system changes its own weights or prompts; too strong |
| Implicit Fine-Tuning Effect (IFTE) | closest to the user's phrase | literally claims fine-tuning; misleading in docs and support |
| Experience-Tuned Adaptation (ETA) | experience-driven behavior | vague; acronym collision |
| Interaction-Tuned Personalization (ITP) | preferences from interactions | narrower than workflow adaptation; misses task strategy |
| Adaptive Familiarity (AF) | the feeling of being known | not measurable; anthropomorphic |
| Fine-Tuning Illusion | the perception itself | describes deception; reject for product copy |

Recommendation: **Adaptive Tuning Effect (ATE)** as the product and evaluation term, **In-Context Adaptation (ICA)** as the engineering term, and **эффект адаптивной настройки** with **«эффект дообучения»** as the user's shorthand.
Product copy may say "`hint` tunes itself to how you work"; it must not say "we fine-tune on your data".

## Signals

| Class | Examples | Trust | Use |
|---|---|---|---|
| Explicit | "remember this", "always/never", a pinned preference, a selected profile, feedback on an answer | highest | write or update a preference immediately |
| Correction | an edit to an answer, undo, a rephrase, the same instruction in a later session | high when it repeats | propose a preference or rule after a threshold |
| Tool and permission | "always allow" for a command, a denied tool, a chosen test command | medium | change defaults, never widen a permission silently |
| Style | language, length, formatting, code-block use, explanation depth | medium | response style, scoped and visible |
| Repository | `HINT.md`, tests, lint, commit style, existing file layout | high for the project | project preferences; most of this already exists as rules |
| Model output | self-critique, a rule the model writes for itself | none | never apply without an explicit user signal |

Signals differ by scope: a preference can be session-local, project-local or global.
Project-local is the default for repository conventions; global is for language, tone and workflow habits.
An inferred preference never crosses from one project to another without the user making it global.

## Preference model

A preference is a small record, not a transcript:

| Field | Meaning |
|---|---|
| `key` | a stable dimension such as response language, verbosity, test command, approval threshold or formatting |
| `value` | the chosen behavior, with a human-readable label |
| `scope` | session, project or global |
| `confidence` | derived from the evidence class, repetition and recency |
| `evidence` | a short list of what produced it, with session or turn references |
| `source` | explicit, correction, tool choice, style or repository |
| `created`, `last_seen`, `expires` | lifecycle, so stale inference decays |
| `state` | proposed, active, pinned or rejected |

Rules for conflict and decay:

- Explicit beats inferred, and project beats global for a project-specific key.
- A single implicit signal proposes; repetition activates; an explicit statement activates immediately.
- Implicit preferences decay with a half-life (starting at 30 days); explicit and pinned preferences do not decay until removed.
- A conflict is shown, not silently resolved; the user makes the last call.
- The prompt carries a compact selected set, capped at a small fixed token budget (starting at 1–2% of the context window), never the whole store.
- Relevant means matching the task, tools and files in the turn; a preference about tests should not ride along on a prose question.

The store starts as a small local file or the existing local database, separate from the session log and the workspace index.
It is append-only in spirit: evidence is added, a preference is superseded, nothing is silently rewritten.
It is never uploaded, and it is included in the export/reset surface.

## Adaptation loop

1. **Observe** the interaction: an explicit instruction, a correction, a tool choice or an accepted answer.
2. **Infer** a candidate preference or a rule, with a source and a confidence.
3. **Propose** it when the evidence is weak, or apply it when the signal is explicit.
4. **Apply** it through the compact prompt block or, for a rule, through an approved file.
5. **Evaluate** whether the correction recurs; a preference that does not reduce friction is retired.
6. **Decay or forget** on schedule, on conflict or on request.

The loop must be interruptible at every step.
A user can see why a preference exists, pin it, edit it, scope it, delete it or turn the whole effect off.
The TUI surface is `/preferences` with `why`, `pin`, `forget`, `scope`, `export` and `reset`; the line REPL gets the same commands.
The core exposes the store through RPC only if a shell needs it; the TUI is a client like every other shell.

## Guardrails

- Local-only by default; no training on user data, no upload, no cross-device sync.
- Explicit consent for inferred preferences; a quiet notice when one activates, not a modal.
- No cross-project leakage; project scope is the default for repository conventions.
- No learning from model output alone; user signals are the only evidence that can activate a preference.
- A confidence threshold and a repetition threshold before an inferred preference applies.
- A prompt budget and a relevance filter; the preference block must not grow without bound.
- Explainability: every active preference names its evidence and its scope.
- Deletion is real: removing a preference removes its evidence from the prompt and the store.
- Prompt injection cannot write preferences; only user actions and reviewable inferences can.
- A kill switch (`preferences.enabled: false`) returns the product to today's behavior exactly.
- Security: no secrets in preferences, redaction on the way in and out, and no preference text in debug logs beyond its key and scope.

## Metrics

| Metric | Definition | Desired direction |
|---|---|---|
| Instruction repetition rate | the same instruction or correction repeated in a later turn or session | down |
| Correction rate | edits, undos or rephrases per answer | down |
| Time to accepted answer | from the question to the answer the user keeps | down |
| Preference hit rate | applicable tasks where a relevant active preference was present | up, without over-application |
| Prompt cost | tokens spent on the preference block per request | bounded |
| Tool calls per task | calls needed to finish the same task | down or stable |
| Regression rate | tasks that get worse after a preference activates | down |
| Unwanted personalization | reports of a preference applied where it does not belong | down |
| Reset/delete rate | how often users remove inferred preferences | a trust signal, not a failure by itself |

The primary metric is correction recurrence, not a self-reported feeling of being understood.
The effect is real only when the behavior changes and the user keeps the result.

## Evaluation plan

- **Offline replay:** build a fixture from recorded sessions, extract candidate preferences, and ask whether each would have prevented a known correction.
  A human reviews the sample.
- **Opt-in A/B.** Randomize whether inferred preferences are applied, with correction recurrence as the primary metric and regression, prompt cost and unwanted personalization as guardrails.
- **Longitudinal dogfood.** Four weeks on the author's own repositories, with a weekly note on what activated, what was wrong and what was deleted.
- **Stop rules.** Disable or roll back a preference class when its prompt cost exceeds the measured savings, when regressions pass the threshold, or when users delete more than they keep.
- **No vanity metrics.** Number of preferences stored, "personalization score" and similar are not success metrics.

## Staged delivery (candidate, not committed)

1. **Explicit preferences and the prompt block.** `/preferences`, a compact selected set, scope and reset; no inference yet.
2. **Implicit evidence and confidence.** Correction and style signals, a review queue, decay, conflict display.
3. **Rule induction with approval.** Propose a project rule after repeated corrections; the user approves it before it is written.
4. **Evaluation and A/B.** The harness above, with the guardrails and stop rules.
5. **Actual weight training:** a non-goal for this plan; revisit only if ICA stalls, the user opts in, and a local adapter beats ICA on the same metrics with a credible privacy model.

The first two stages are useful without any model change and fit the existing preamble rebuild.
Stage 3 must not write to `.hint/rules` without an explicit approval, and must never overwrite a user-authored rule.

## Alternatives considered

| Alternative | Why not now |
|---|---|
| Do nothing and rely on `HINT.md` and rules | Cheapest and fully explicit, but every new project repeats the same setup and the tool never notices a correction |
| Cloud personalization | Better cross-device recall, but it breaks the local-first promise, creates lock-in and asks for the user's data |
| Actual fine-tuning (LoRA or similar) | Stronger adaptation in principle, but needs data, consent, compute, versioning and a privacy model; ICA is cheaper to test and explain |
| Hidden prompt mutation | Cheap to build, impossible to explain or audit, and prone to self-reinforcement |
| Let the model write its own rules | Convenient, but a document or a bad turn could inject a rule; approval is the minimum |
| Explicit-only preferences | Transparent and safe, but the friction is exactly what ATE is supposed to remove |

## Open questions

- Does the preference store belong to the memory/index work, or to a new `internal/preferences` package with no retrieval dependency?
- How much inference is enough before the user feels understood, and how much is too much?
- How do we evaluate counterfactuals without a large user base?
- Should project preferences be shareable in the repository (a reviewed file) or stay local?
- How does ATE interact with profile and model switches, offline fallback and compaction?
- Is actual fine-tuning ever worth it once ICA has a measured baseline?
- Where does the roadmap put stage 1: after the Phase 3 multimodality work, or a dedicated phase before it?

## Decision

Working terms: **Adaptive Tuning Effect (ATE)** for the product effect and **In-Context Adaptation (ICA)** for the mechanism.
The effect must be earned by measurable behavior, not claimed in marketing.
No code starts before Phase 2R accepts this design, resolves Q12 and names a work package with an evaluation gate.

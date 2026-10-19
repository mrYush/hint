# Completion quality and cost evaluation

> Status: **backlog** (decided 2026-10-19) · Does **not** gate `v0.3`.
> Feeds one decision: whether any `completion.mode` other than `off` may
> ever be a default, and which small models the README may recommend.
> Until it has run, completion stays off by default
> ([WP1.6](phase-1-core-service-tui.md#wp16--completion-of-what-the-user-is-typing--done-2026-10-19)).

## Why

WP1.6 shipped completion of what the user types without a single run
against a real model: the prompt and the post-processing
(`trimSuggestion` in `cmd/hint/rpccomplete.go`) were checked on a fake
provider and a table of cases. Its review (2026-10-19) named that the one
open risk: on a 1–3B model the suggestions may be useless, while every
request still spends tokens and, with a cloud profile, sends the typed
text off the machine. The feature's only guard today is `off` by default.

## What is measured

One run is one model on one machine against a fixed set of questions,
recorded the way `pi-bench` records a run
([`evidence/pi-4-20261011-105151/`](evidence/pi-4-20261011-105151/summary.md)).

- **Acceptance rate.** The share of prefixes for which the suggestion is
  the continuation a developer would take as is, judged by hand against
  the full question (the set below). The number that decides anything.
- **Usable rate.** The share where the suggestion is on topic and at
  least one word of it would be kept after editing. Shows whether a
  better prompt could help, as opposed to a bigger model.
- **Empty rate.** The share answered with nothing. A high number with a
  low acceptance rate means the model gives up; a high number with a high
  acceptance rate is fine.
- **Latency.** Median and p90 from `complete` to its answer, as the TUI
  waits for it; the `warn_after` default (2 s) is judged against the p90.
- **Cost.** Input and output tokens per request from `usage`, and the
  total for the set, as `/completion` would report it.
- **Residency.** With Ollama: whether the main profile's model was evicted
  by the completion model (`ollama ps` before and after), and the reload
  time of the main model if it was. This is the "second model on a small
  machine" warning of the README, measured.

## The question set

Twenty questions a developer types to `hint` in a Go repository, each cut
at two points: after the first three words and after about two thirds.
Forty prefixes. The set lives beside the evidence once written
(`evidence/completion-<date>/questions.jsonl`: `{"question","cut","prefix"}`),
so a later run on another model compares against the same prefixes. The
questions cover the shapes the shell sees: "what does X do", "where is X
tested", "add a flag that", "why does the test fail when", "explain the
difference between", a question continued from an earlier answer (the
session's last question is part of the request).

## Models and machines

| model | where | why |
|---|---|---|
| `qwen2.5:0.5b` | Ollama, laptop | the floor: is anything this small usable at all |
| `qwen2.5:1.5b` | Ollama, laptop and Pi 4 (`llama-server`, Q4_K_M) | the candidate the README would name |
| `qwen2.5-coder:1.5b` | Ollama, laptop | whether a code model does better on developer questions |
| the session's own model (e.g. `Qwen2.5-Coder-32B` behind vLLM) | the inference host | the default (`completion.profile` empty): quality is highest, latency and cost are the question |

A run against a cloud aggregator is optional and only measures cost and
latency: its quality is not in doubt, its price per keystroke pause is.

## How to run

1. Build `hint` from the commit under test; note it in the summary.
2. A config with `completion.mode: tab`, `completion.profile` pointing
   at the model under test, `warn_after: 10s` so nothing is cut short.
3. A script over `hint --mode rpc`: `hello`, `session.open`, one
   `turn.start` per question that has a preceding one (so the last
   question is in context), then `complete` for each prefix, recording
   the suggestion, `usage` and the wall time. `docs/rpc.md` has the
   messages; `scripts/pi-bench/` is the shape of the script and of
   `summarize.py`.
4. Judge the suggestions by hand into accepted / usable / empty / wrong,
   in `runs.jsonl`, and let the summary compute the rates.
5. Write `evidence/completion-<date>/summary.md` with the environment
   table, the rates, the latency and cost numbers, and residency.

## Pass criteria

- A model may be named in the README as a completion model when its
  acceptance rate is at least 40 % and its p90 latency under 2 s on the
  laptop. Below that, the README keeps saying "the session's profile".
- `tab` may be discussed as a default only if the session's own model
  reaches 60 % acceptance and the median cost of a request is under 300
  tokens; `idle` is not discussed as a default at all until a request
  budget exists in the core (also backlog, from the same review).
- Residency: if the completion model evicts the main model on the Pi 4,
  the README's warning stays and the Pi guide says so in its own words.

## Out of scope

Prompt tuning beyond two or three variants of `completionSystem`; a
prefix cache of suggestions; inline ghost text. Each is its own backlog
item in WP1.6's decisions.

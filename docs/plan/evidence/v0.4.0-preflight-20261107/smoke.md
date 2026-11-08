# Smoke procedures

Set `BIN` to the absolute path of the recorded executable and `FIXTURE=$(sh fixtures.sh)` to a fresh scratch tree for each scenario.
Run from `"$FIXTURE/primary"` with `"$BIN" --plain --provider local --debug`, using a real terminal and a tool-capable local profile.
Use the TUI for A7 and where concurrent input is needed in A3.
Capture terminal and tool traces privately first, then retain reviewed evidence with tool arguments/results, relevant catalog, fixture operations and observed status.
Use the exact cursor returned by the trace in place of each placeholder.
Capture actual tool errors rather than inferring them from the model's answer.

## A1

Enter `/repos index on` and `/repos add ../second-fixture second`.
Prompt: “Call repo_map with repo second and limit 1. Then call repo_list. Report the map scope and source locations, both root IDs and the membership revision.”
Remove second with `/repos remove second`, reattach it with the same add command, then prompt: “Call repo_map with only cursor <OLD_CURSOR>, once. Report the exact tool error.”
Compare root IDs and revision before and after.

## A2

Enter `/repos index on`.
Prompt: “Call repo_list and inspect the primary tree. Call repo_map on primary, then on subtree nested in primary. Try read of nested/hidden.go and try editing it through primary. Report each actual result.”
Enter `/repos add ./nested nested`.
Prompt: “Call repo_map on nested, then call repo_map on primary for LookupBeacon. Report the source locations.”
Approve only the disposable fixture edit attempt, if asked.

## A3

Enter `/repos index`, then `/repos index on`.
Prompt: “Call repo_map for LookupBeacon and report the result.”
Enter `/repos index off`.
Prompt: “Use the available tools to locate LookupBeacon and read alpha.go. Report whether repo_map was available.”
Restart and enter `/repos index`.
Start a real streaming turn and submit `/repos index` and `/repos index off` before it ends.
Capture turn timing, both status results and actual provider catalogs.

## A4

Enter `/repos index on`.
Prompt: “Call repo_map for Mutable and report the freshness and signature. Call repo_map for the whole primary root with limit 1 and save the cursor.”
In a second terminal in the fixture primary, run `cp -p methods.go methods.stamp; sed 's/old:/new:/' methods.stamp > methods.go; touch -r methods.stamp methods.go; rm methods.stamp`.
Prompt: “Call repo_map for Mutable again and show the signature and freshness; then read methods.go and show its current return expression.”
Repeat with a signature change and deletion, and capture raw versions and membership/policy/generation evidence.
Use membership change to establish a definitive invalidation; generation advancement alone can preserve a retained cursor under the contract.
For transient I/O, make a relevant continuation source temporarily unreadable as the non-root fixture user, capture the actual transient error, restore permissions and retry the same cursor.
If that platform/path cannot induce the required transient error, record that subcase as blocked with the attempted reproduction.
For partial empty coverage, retain a returned empty page with `query_complete: false` from a bounded search with filtered/invalidated candidates and verify continuation; increase the fixture size if necessary and record its hash.
Do not substitute a complete empty page.

## A5

Enter `/repos index on` and `/repos add ../second-fixture second`.
Prompt: “Call repo_map with repo second and limit 1 and return the cursor.”
Rename from the fixture parent with `mv second-fixture second-hidden`.
Prompt: “Call repo_map with repo second and report the exact outcome.”
Restore with `mv second-hidden second-fixture`.
Prompt: “Call repo_map with repo second. Then call repo_map with only cursor <OLD_CURSOR>. Report both exact outcomes.”

## A7

Use the TUI and preserve session IDs with `"$BIN" sessions` from the fixture primary.
Attach `/repos add ../detached detached`, detach with `/repos remove detached`, and send “Reply exactly FIRST.”
Attach `/repos add ../second-fixture second`, then send “Reply exactly SECOND.”
Run `/new` then `/repos`.
Restart with `"$BIN" --session <FIRST_SESSION_ID>` and enter `/repos`.
In the TUI message browser press Esc from empty input, select FIRST, and press `f`.
Inspect `/repos` and the historical detached-root reservation.
Rename second away, resume the original again, and capture inactive-root diagnostics.

## Identity

In the empty `"$FIXTURE/identity"` directory run `"$BIN" --provider local --no-session -p 'кто ты?'`.
Use the selected live profile and retain the answer with its model/runtime and executable hashes.

# The pipeline: scope in, findings out

How a target travels through HuntDeck, which code owns each hop, and which
test holds the hop shut. Written so a change in one link cannot silently cut
another: every link here has a guard that fails loudly when the link breaks.

```
platforms ──poll──▶ changes ──▶ hunt_queue ──handoff──▶ ScopeSentry
                                      │                     │
                                      ▼                     ▼
                                 notifications ◀──── Summary{tasks, targets, node, programs}
```

## 1. Scope arrives (poll → changes → store)

Polls fetch programs and scope; the diff engine records what changed. Scope
entries land in `scope_entries`; the scanner never reads this table directly —
the handoff does.

## 2. The queue ranks targets (internal/store/queue.go, internal/huntqueue)

`QueueCandidates` joins `target_scores` and — since this week — `scan_handoffs`:
a target the scanner already holds carries the reason "already handed to the
scanner". The score itself is unchanged: scanned is not worthless.

- Guard: `TestQueueMarksHandedOffTargets` — the handed-off target is in the
  queue with the marker.
- The backfill is FIFO (`ScanBacklog`, oldest first): newest-first meant one
  program was scanned ten times while a backlog of hundreds never drained.
- The stall is visible: `StalledFor` renders on /scan and in `doctor`
  (`TestStallFor`); a backlog stuck for two hours says so.

## 3. The handoff (internal/scanhandoff)

`RunWith` is the only door. In order: config → headroom (`CheckHeadroom`) →
node (`chooseNode`) → FIFO candidates → the second opinion (`runOpinion`) →
per-program tasks (`groupByProgram`, `taskName`) → **record before start**
(`MarkHandedOff`) → start. Returns a `Summary{Tasks, Targets, Node, Programs}`.

- Both entry points attach the decision model — the page (`internal/web/scanhandoff.go`)
  and the CLI (`cmd/huntdeck/caps.go`). A batch the model blocks is not created;
  `TestASecondOpinionStopsTheBatch` holds block and confirm shut.
- Record-before-start is the load-bearing line: thirteen tasks once sat parked
  while their targets stayed eligible, so the next handoff re-created them.
- Tests that hold each rule: `TestHandoffSendsEachTargetOnce`,
  `TestTasksCarryOneProgramAndSayWhich`, `TestOnlyTargetsFindsAUrlEntryByItsHost`,
  `TestChooseNodeReportsBusyWhenEveryNodeIsFull`.

## 4. The report (internal/notify/handoff.go)

`ReportHandoff` turns a `Summary` (or the error that stopped the batch) into
`scan_handoff` / `scan_blocked` events and queues them through `route` — the
same path a poll uses, so instant channels fire and digest channels collect.
Best effort: a webhook being down never fails a handoff. The serve loop and
the page call it; the CLI prints to stdout instead.

- Tests: `TestReportHandoffQueuesForMatchingChannels`,
  `TestReportHandoffReportsABlock`, `TestReportHandoffDigest`.

## 5. Where a reader sees it

- /scan — waiting targets, recent handoffs with task ids, the stall warning.
- /queue — the score, the reasons, and the "already handed to the scanner" marker.
- /match — a scope check links to the queue and the scan page.
- /programs/{p}/{h}?tab=scope — links to the queue (platform-filtered) and /scan.
- `huntdeck doctor` — the handoff line: nothing handed over yet / last handoff /
  stuck for N hours.
- Notifications — two event kinds, routed by each channel's rules.

## The last link, now closed

**Scan result → store.** The open link was here: `get_task_progress` (the tool
the handoff first used) returns module timings and no status, so a finished or
failed task was known to the scanner alone. `get_task` does carry it - status,
progress and the end time - which was checked against a real task before the
column landed:

- migration 0078 adds `state`, `status` and `checked_at` to `scan_handoffs`;
- `scanhandoff.Reconcile` reads the newest tasks and keeps the outcome, once
  per task (a finished task is not asked about again),
- it runs with the handoff on the same clock in `serve`, and on demand as
  `huntdeck scan-reconcile [-limit N]`,
- a task the scanner no longer knows is recorded as `gone`; a network or auth
  failure stops the pass rather than marking everything gone.

Guarded by `TestReconcileRecordsWhatTheScannerSays`: the finished task is kept
as done, the forgotten one as gone, and a second pass does not ask about the
finished task again (removing that skip turns it red).

## The failure catalogue (what each link taught)

| Link broke by | Symptom | Guard now |
|---|---|---|
| newest-first batch | one program scanned 10×, backlog starved | FIFO + `TestScanBacklogDrainsOldestFirst` |
| record after start | parked tasks, re-created targets | record before start + `TestASecondOpinionStopsTheBatch` |
| no second opinion on the CLI | blocked batches went out anyway | both runners attach it |
| silent confirm() | every destructive button did nothing | in-page dialog (`askInPage`) |
| reasons truncated to two | the marker never rendered | marker is the first reason |
| looped twice while busy | outbox skipped digest channels | `route` for both paths |

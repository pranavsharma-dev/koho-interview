# Build log

**Total time:** _h _m (fill in at the end, honestly; say so if over four hours)
**Before the clock:** Planned stack, schema, transfer design and test strategy with Claude
(chat) before starting. During that planning the assistant's first transfer design claimed
the idempotency key before locking accounts; running it against Postgres deadlocked, and
the design was changed to lock first. I verified the fix in my own tests (test 7 / 10).
**Stack:** Go (Go gate: Go was not installed; installed Go 1.27.0 via `winget install --id GoLang.Go`. Docker Compose v5.1.4 was already present.)

## Timeline

| Clock | Milestone | Notes |
| --- | --- | --- |
| 0:00 | Start | 2026-10-12 14:56 PDT |
| 1:03 | Skeleton green | 2026-10-12 15:59 PDT. `go build`/`go vet` clean; `TestConnect` passes against real Postgres, including `-race` (run inside a `golang` container since no C compiler was available on the host yet — see below). |
| 1:09 | Accounts | 2026-10-12 16:05 PDT. POST /accounts, GET /accounts/{id}, error envelope. Tests 1 and 5 (account part) pass, incl. -race. |
| 1:28 | Transfer core | 2026-10-12 16:24 PDT. Store.Transfer: lock both rows ORDER BY id FOR UPDATE, insert transfer row, check locked balance, relative debit/credit. No idempotency dedup yet (Phase 5). Tests 2-6 pass, incl. -race. |
| 3:03 | Test 7 seen failing, restored | 2026-10-12 17:59 PDT. Broke the lock on purpose (removed ORDER BY/FOR UPDATE, switched to absolute writes), ran test 7 (50 iterations), saw it fail on 49/50. Restored; `git diff` confirmed byte-identical to the last commit; full suite green again incl. -race. |
| 3:15 | Idempotency | 2026-10-12 18:11 PDT. `ON CONFLICT (idempotency_key) DO NOTHING RETURNING`, placed after the lock (not before — that's the exact mistake noted in "before the clock"). No-row case SELECTs the existing transfer and compares from/to/amount: match -> replay (200), mismatch -> ErrKeyReused (422). Tests 8, 9, 10 pass; test 7 re-ran green (no regression), incl. -race. |
| | Atomicity test | |
| | History | |
| | Adversarial review | |
| | Docs, fresh-clone check | |

## Plan

Invariants first, tests as the proof. Cut order if behind: optional tests, deterministic
lock-holder test, history ordering, test 11, history. Never cut: test 7 with break-it,
tests 8 and 9, README, this log, fresh-clone check.

## Where AI helped

- **Schema-never-applied gap (Phase 2).** `main.go` applies `schema.sql` on boot
  (by design, per CLAUDE.md), but the server had never actually been *run* —
  only `go test`, which doesn't touch schema. Every handler test failed with
  `relation "accounts" does not exist`. Rather than move schema application
  into the test helpers (would've duplicated/contradicted main.go's documented
  job), fixed it at the right layer: `make db` now also pipes `schema.sql`
  into the container via `psql`, so `make db && make test` works standalone on
  a fresh clone without requiring `make run` first. Good interview
  story: a real bug caught by an honest error message, not a guess, and fixed
  by asking "whose job is this, architecturally" rather than patching the
  symptom.
- **Silently-ignored encode errors (Phase 2 review).** `writeError`/`writeJSON`
  called `json.NewEncoder(w).Encode(...)` and discarded the error — technically
  fine 99% of the time, but a direct violation of CLAUDE.md's explicit "never
  ignore an error" rule. Caught on a deliberate second read-through, not by a
  test (nothing would have failed) — a reminder that some invariants need
  code review, not just test coverage.
- **Windows cgo/-race chain.** `go test -race` needs `cgo`, which needs an
  actual C compiler, because Go's race detector is built on Google's
  ThreadSanitizer (a C/C++ library linked into the binary) — nothing to do
  with our code, a pure toolchain fact. No compiler was on this machine; the
  WinLibs MinGW-w64 GCC install via winget took 30+ minutes (tens of thousands
  of tiny files, each scanned by Windows Defender on extraction). Workaround:
  ran `-race` inside a disposable `golang:1.25` Docker container on the same
  Docker network as the Postgres container, which ships `gcc` preinstalled —
  seconds instead of tens of minutes, and it's what Phases 1-4 actually used
  for every `-race` run. The native GCC install finished eventually too and
  is available now, but the container path turned out to be the practical one
  under time pressure.
- **Which mechanism actually serializes a concurrent duplicate (Phase 5,
  worth having cold for the interview).** SPEC.md's duplicate-cases table
  says a concurrent same-key request "waits on the unique index." True in
  general, but in test 10 (ten goroutines, same key, same from/to accounts)
  that's not actually the first thing they wait on: since every request in
  that test targets the *same two account rows*, they all serialize on the
  `FOR UPDATE` row lock first. By the time the 2nd through 10th goroutines
  each get the lock in turn, the 1st has already committed and the key row
  is already visible — so they each take the clean "replay" path at the
  INSERT, never actually blocking on the unique index at that point. The
  unique-index wait is the real mechanism only for the more general case
  (same key, and — hypothetically — different accounts), which can't happen
  here since a key is 1:1 with one sender/receiver pair by construction. Both
  explanations are true; asked "so which one is it," the honest answer is
  "the row lock dominates in this schema's access pattern, the unique index
  is the backstop that would still make it correct even if it didn't."
  Traced through Postgres's MVCC/READ COMMITTED per-statement snapshot rules
  to confirm there's no race window in the replay SELECT (by the time our
  `ON CONFLICT DO NOTHING` sees zero rows, the conflicting transaction is
  guaranteed already committed, not just in-flight — otherwise our own
  INSERT would have gone through instead).

## Where it was wrong and how I caught it

(file, symptom, the test or reading that exposed it. Only real ones.)

Nothing design- or code-wrong yet in the money-transfer logic itself (Phases
1-5 implemented SPEC.md's already-detailed design faithfully — the two real
issues above were both environment/process, not transfer correctness). Will
log honestly here the moment something actually happens rather than invent a
story for the interview.

- 

## Test 7 break-it result

Change made in `internal/bank/store.go`'s `Transfer`: dropped `ORDER BY id FOR UPDATE`
from the lock query (plain `SELECT ... WHERE id IN ($1, $2)`, no lock at all), and
replaced the two relative `UPDATE ... balance_cents = balance_cents ± $1` statements
with absolute writes computed in Go from the unlocked read
(`newFromBalance := balances[fromID] - amountCents`, etc.) — exactly the
check-then-write pattern SPEC.md warns about.

Ran `go test ./internal/api/... -race -count=1 -run TestCompetingTransfersNeverOverdraw`
(inside the `golang:1.25` container — see the Go/cgo section below for why). Real output,
trimmed to the first few failures and the summary (49 of 50 iterations failed identically;
only iteration_0 happened to pass, presumably because that one's two goroutines didn't
land close enough in time to actually race):

```
--- FAIL: TestCompetingTransfersNeverOverdraw (1.38s)
    --- FAIL: TestCompetingTransfersNeverOverdraw/iteration_1 (0.02s)
        competing_transfers_test.go:69: expected exactly one 201 and one 409, got statuses [201 201]
    --- FAIL: TestCompetingTransfersNeverOverdraw/iteration_2 (0.03s)
        competing_transfers_test.go:69: expected exactly one 201 and one 409, got statuses [201 201]
    --- FAIL: TestCompetingTransfersNeverOverdraw/iteration_3 (0.03s)
        competing_transfers_test.go:69: expected exactly one 201 and one 409, got statuses [201 201]
    ... (iterations 4-49 all identical: both requests got 201)
FAIL
FAIL	github.com/pranavsharma-dev/koho-interview/internal/api	1.419s
FAIL
```

Both racing requests read A's balance as 1000 before either had committed, so each
independently judged the transfer affordable and computed its own "balance - 700"
from that same stale snapshot. Both got 201. Whichever write lands last wins, so the
other transfer's debit is silently lost — this is the "700 cents created from nothing"
case SPEC.md describes: not a negative balance (invariant 1's CHECK constraint never
fires), only a total-money assertion catches it. In practice the test caught it even
earlier, on the status-code assertion, since the broken version never produces the
required 409 at all.

Restored the original lock query and relative updates immediately after. `git diff`
showed the file byte-identical to the last commit; full suite re-ran green, including
`-race`.

## What I rejected and why

- 

## What I am uncomfortable with

- Test seam in Store.Transfer exists only for the atomicity test.
- Idempotency keys are global and never expire.
- Test 7 is probabilistic; it does not prove every interleaving or two server instances.
- **Phase 4's break-it exercise was done entirely by the AI, not by me.**
  CLAUDE.md's own plan for this phase says "I will break the lock on purpose
  to see it fail; help me do that and restore it" — I'd intended to drive that
  part hands-on, but delegated the whole build-break-capture-restore cycle to
  Claude instead, continuing the rhythm from the last few phases. The result
  is correct and the real failing output is captured above, but I didn't get
  the hands-on rep. Re-derived it myself afterward by re-reading the diff
  rather than re-running it live — worth actually re-breaking it once more
  before the interview so the whiteboard version isn't secondhand.

---

### Scratch: AI was wrong / I rejected (write the moment it happens)

- 

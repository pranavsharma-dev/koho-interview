# Build log

**Total time:** 3h 37m clocked (14:56-18:33 PDT), under the 4-hour cap. Honest breakdown:
roughly 1h30m of that (between the "Transfer core" and "Test 7" rows below) was environment
setup, not feature work — installing Go, then discovering `-race` needs `cgo` needs an
actual C compiler, installing a Windows GCC toolchain (slow: tens of thousands of tiny
files, each scanned by Defender), and installing `make`. Worked around the slow GCC install
by running `-race` in a disposable `golang` Docker container for the rest of the session
rather than waiting on it. If I were doing this again on a machine I controlled, I'd
pre-install Go/a C toolchain/Docker before starting the clock — all of Phase 0's "before the
clock" checklist, which I partly skipped early on. That's the honest "what ate the time"
answer, not padding the feature-work total.
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
| 1:28 | Transfer core | 2026-10-12 16:24 PDT. Store.Transfer: lock both rows ORDER BY id FOR UPDATE, insert transfer row, check locked balance, relative debit/credit. No idempotency dedup yet (Phase 5). Tests 2-6 pass, incl. -race. Code and tests were green at this clock mark; the commit itself (`be331c7`) is timestamped 17:50 PDT, after the GCC detour below — held off committing until `-race` could actually run against it. |
| 3:03 | Test 7 seen failing, restored | 2026-10-12 17:59 PDT. Broke the lock on purpose (removed ORDER BY/FOR UPDATE, switched to absolute writes), ran test 7 (50 iterations), saw it fail on 49/50. Restored; `git diff` confirmed byte-identical to the last commit; full suite green again incl. -race. |
| 3:15 | Idempotency | 2026-10-12 18:11 PDT. `ON CONFLICT (idempotency_key) DO NOTHING RETURNING`, placed after the lock (not before — that's the exact mistake noted in "before the clock"). No-row case SELECTs the existing transfer and compares from/to/amount: match -> replay (200), mismatch -> ErrKeyReused (422). Tests 8, 9, 10 pass; test 7 re-ran green (no regression), incl. -race. |
| 3:20 | Atomicity test | 2026-10-12 18:16 PDT. Unexported `midTransferHook func() error` on Store, nil in production, called between debit and credit. Test 11: injected failure -> both balances unchanged, no transfer row, key not consumed, same key then succeeds normally. Full suite incl. -race green, test 7 re-confirmed. |
| 3:31 | History | 2026-10-12 18:27 PDT. GET /accounts/{id}/transactions: 404 check via EXISTS, then ORDER BY id DESC with direction/counterparty computed relative to the queried account. Test 12 (newest-first, correct direction/counterparty/amount) plus a 404 case pass; full suite incl. -race green. |
| | Adversarial review | Deferred to a fresh session, by design — see Phase 8 entries below. |
| 3:37 | Docs, fresh-clone check | 2026-10-12 18:33 PDT. `git clone` into a scratch dir, own Postgres container, schema applied from scratch, full suite incl. -race green on the clean checkout. README written. This log finished. |
| — | Phase 8: Adversarial review | 2026-10-12, separate fresh-context session (by design — PLAN.md calls for this so the review isn't biased by having built everything myself), ending 19:22 PDT. Findings below. |

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
- **The test seam isn't actually thread-safe, and that's fine (Phase 6).**
  `midTransferHook` is an unexported, unsynchronized function field on
  `*Store`. Mutating a shared struct field without a lock while other
  goroutines might be reading it is exactly the kind of thing `-race` exists
  to catch. It stayed clean here only because test 11 deliberately
  constructs its own private `Store` instance rather than reusing the
  `httptest`-backed shared one that the concurrency tests (7, 10) hit — so
  there's never a second goroutine touching that field. If I ever needed to
  inject a failure into a `Store` already serving concurrent traffic, this
  field wouldn't be safe as-is; I'd want an atomic pointer swap or a
  per-request context value instead. Worth saying out loud if asked "is
  this thread-safe" rather than letting "the race detector didn't complain"
  stand in for "I checked why."
- **History query doesn't actually use its own indexes for the sort (Phase
  7) — checked, not assumed.** `schema.sql` has `transfers_from_idx
  (from_account_id, id)` and `transfers_to_idx (to_account_id, id)`,
  seemingly set up so `ORDER BY id` comes pre-sorted from either index. But
  the history endpoint needs *both* directions in one list
  (`WHERE from_account_id = $1 OR to_account_id = $1`), and `OR` across two
  different-column indexes can't preserve either index's row order. Ran
  `EXPLAIN` against the real table to check rather than assume: Postgres
  does a `BitmapOr` of both indexes (correct for the filter) followed by an
  explicit `Sort` node (the indexes don't help with the `ORDER BY id DESC`
  at all once combined). Not a correctness bug — just means these two
  indexes are sized for "all transfers in one direction for one account,"
  not for "this account's full history," which is the one query this
  endpoint actually runs. Fine at this scale; a composite/covering index or
  a UNION ALL of two indexed sub-queries would be the fix if history pages
  got large. Good "what would you do with more time" answer.
- **Two real coverage gaps, found by trying to exercise mechanisms that were
  only ever asserted, not tested (Phase 8).** Test 10 (ten goroutines, one
  key, one account pair) is dominated by the `FOR UPDATE` row lock — every
  goroutine serializes on that before the unique index on `idempotency_key`
  ever matters, so the "concurrent duplicates wait on the unique index"
  mechanism SPEC.md's duplicate-cases table describes was never actually the
  thing under test. Added `TestTransferConcurrentSameKeyDisjointAccounts`
  (same key, two disjoint account pairs — nothing to serialize on but the
  index) to hit that path for real; passes. Same story for `ORDER BY id`
  preventing opposite-direction deadlock: confidently stated in INTERVIEW.md,
  never tested. Added `TestOppositeDirectionTransfersNoDeadlock` (SPEC's
  optional test 14, 20 iterations with a timeout). Also passes. Both now in
  `internal/api/adversarial_test.go` (not yet committed — see "uncomfortable
  with" below).
- **`go.mod`'s version floor didn't match what the docs promised (Phase 8) —
  and my first fix made it worse.** `go.mod` declared `go 1.25.0`;
  `CLAUDE.md` and `README.md` both said "Go 1.22+". Since Go 1.21 the `go`
  directive is a hard build-time floor, not a hint, so this was a real gap —
  a reviewer with Go 1.22-1.24 would've hit a build error the docs said
  wouldn't happen. First instinct was to lower `go.mod` to `1.22` to match
  the docs. `go mod tidy` immediately reverted it back to `1.25.0` on its
  own — turns out `github.com/jackc/pgx/v5@v5.11.0` (our actual Postgres
  driver, not a stray transitive dep) declares `go 1.25.0` in its own
  `go.mod`, and Go's module graph takes the max floor across all
  dependencies. `1.22` was never actually achievable here. Correct fix was
  the opposite direction: bumped `CLAUDE.md`, `README.md`, and `PLAN.md` to
  say "Go 1.25+" to match the real, enforced floor. Good reminder that
  "the docs and the code disagree" doesn't tell you which one is wrong —
  checking who actually enforces the constraint before "fixing" it both
  ways would have cost nothing and I nearly skipped it.

## Where it was wrong and how I caught it

(file, symptom, the test or reading that exposed it. Only real ones.)

Nothing design- or code-wrong in the money-transfer logic during the
original build (Phases 1-7 implemented SPEC.md's already-detailed design
faithfully). Phase 8, below, is where real findings showed up.

- **A break-it test that didn't actually break (Phase 8).** To sanity-check
  the new opposite-direction-deadlock test, I removed `ORDER BY id` from the
  lock query in `Store.Transfer` and re-ran it — same discipline as the Test
  7 break-it check. It kept passing. Before writing that off as "the test is
  weak," I checked why with `EXPLAIN`: Postgres's planner picks an `Index
  Scan` on `accounts_pkey` for this query regardless of `ORDER BY`, and that
  scan happens to emit ascending-id order either way at this table size — so
  removing the clause didn't change observed behavior today. Confirmed with
  `SET enable_indexscan = off` that this really is a planner artifact, not a
  SQL guarantee: nothing requires ascending order without an explicit
  `ORDER BY`. The code is correct and `ORDER BY id` stays — it's the only
  real guarantee — but my first attempt at proving that with a break-it test
  proved nothing. Logging this so "I broke it and watched it fail" doesn't
  get reused as evidence for a claim it didn't actually test.
- **A review pass's own suggested fix would have introduced a real bug
  (Phase 8).** While adding DB-level upper-bound backstops for the 10^12
  amount cap (matching SPEC.md's own philosophy — CHECK constraints back up
  the lock/validation logic, they don't replace it — and the existing lower
  bounds already in `schema.sql`), a suggestion came back to add
  `CHECK (balance_cents <= 1000000000000)` directly on `accounts.balance_cents`.
  That's wrong: `balance_cents` is a cumulative running total, not a single
  request's input — a legitimately busy account can exceed 10^12 cents after
  enough separately-valid incoming transfers. That CHECK would eventually
  reject a real, correct credit update with an opaque constraint-violation
  error instead of ever applying. Caught it before touching `schema.sql`;
  applied the corrected version instead — bounds only on the two actual
  single-request inputs the cap is meant to validate,
  `opening_balance_cents` and `transfers.amount_cents`.

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

Nothing during the original build session — no AI suggestion got overridden before any
commit (the idempotency-before-lock design error under "Before the clock" was caught and
changed during planning, before the clock started, so there was nothing left to reject
mid-build). Phase 8 (separate session) did have one real rejection:

- **A suggested `CHECK (balance_cents <= 1000000000000)` on `accounts`.** Covered above
  under "Where it was wrong" — would have capped a cumulative running total at a
  single-transaction bound, eventually rejecting legitimate high-volume accounts.
  Rejected; applied the corrected version (bounds on `opening_balance_cents` and
  `transfers.amount_cents` only, mirroring the existing per-request validation).

## Commit honesty

`BRIEF.md` was added during Phase 0 but got bundled into the Phase 1 commit (`ef5160b`,
"feat: project skeleton with postgres and schema") instead of its own
`docs: add original brief` commit, which is what PLAN.md's Phase 0 line actually called
for. Noting it here as promised rather than leaving it unsaid. Not a meaningful hygiene
problem on its own — one doc file riding along with the first real commit — but the kind
of small slip worth being upfront about given BRIEF.md's explicit "we care that the
history is real."

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
- **`internal/api/adversarial_test.go`, the `go.mod` fix, and the `schema.sql`
  CHECK additions from Phase 8 are not committed as of this entry.** Reviewed,
  tests pass, but committing after reading the diff is mine to do, per the
  working agreement — not done automatically on my behalf.

---

### Scratch: AI was wrong / I rejected (write the moment it happens)

- 

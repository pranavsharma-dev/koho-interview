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
| | Idempotency | |
| | Atomicity test | |
| | History | |
| | Adversarial review | |
| | Docs, fresh-clone check | |

## Plan

Invariants first, tests as the proof. Cut order if behind: optional tests, deterministic
lock-holder test, history ordering, test 11, history. Never cut: test 7 with break-it,
tests 8 and 9, README, this log, fresh-clone check.

## Where AI helped

- 

## Where it was wrong and how I caught it

(file, symptom, the test or reading that exposed it. Only real ones.)

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
- 

---

### Scratch: AI was wrong / I rejected (write the moment it happens)

- 

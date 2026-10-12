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
| | Accounts | |
| | Transfer core | |
| | Test 7 seen failing, restored | |
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

```
(paste the real failing output here)
```

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

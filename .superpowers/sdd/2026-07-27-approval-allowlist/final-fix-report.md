# Final Fix Report: feat/approval-allowlist

Date: 2026-07-29

---

## Finding 1 — canRemember/rememberTarget missing from WS approval handler

### Approach chosen: explicit field list with two fields added (option a)

**Why not a bare spread:** `payload.id` maps to `requestId` — a bare spread of the payload would produce `{ id: …, requestId: … }` (both, neither matching the store's expected key). A restructure like `{ ...payload, requestId: payload.id }` would carry unknown future server fields silently — benign for this store, but unpredictable. The reviewer's concern was the opposite: a field-by-field list that _drops_ fields the server adds.

**Why keep the explicit list (option a):** The explicit list is the maintenance surface. If someone adds a field to `NativeApprovalPayload` on the server but forgets to add it here, the omission is _visible as a gap_ — an auditor scanning the handler sees the list and can compare it against the type. A spread-based approach would make the mapping invisible and trade one hazard for another.

**The Korean comment added** names the hazard explicitly and references this bug as the lesson: `canRemember`/`rememberTarget` survived six task reviews precisely because nothing marked the mapping as a maintenance boundary.

### Files changed
- `client/src/hooks/useWebSocket.ts` — added `canRemember` and `rememberTarget` to the `addApproval` call in the `native:approval` handler, with a comment explaining the `id → requestId` rename hazard.

---

## Finding 2 — WS native:decide + Remember:true path untested

### Test approach

The existing `ws` package tests construct `Hub` structs directly (no net/HTTP, no goroutines). I followed the same pattern:

1. `services.NativeService` (real implementation, no process spawned)
2. `services.ApprovalRuleStore` backed by in-memory SQLite (same DB as `services` tests)
3. Two new helpers in the `services` package (not in `_test.go` files, to be callable from the `ws` package):
   - `NativeService.SetPolicyForTest(sessionID, cwd)` — injects a session policy without starting a driver process. Named `ForTest` to signal test-only intent.
   - `PermissionBroker.InjectPending(req) <-chan PermissionDecision` — places a request directly in the pending map and returns the answer channel. Named `InjectPending` to make the test-only purpose self-documenting.
4. Two test cases in `server/ws/hub_native_decide_test.go`:
   - `TestNativeDecideRememberSavesRule` — the main case: snapshot before Decide → rule is stored.
   - `TestNativeDecideRememberRequiresCwd` — edge case: empty cwd → no rule stored.

### Sabotage evidence

**Run 1 — production code, test PASSES:**
```
=== RUN   TestNativeDecideRememberSavesRule
--- PASS: TestNativeDecideRememberSavesRule (0.00s)
=== RUN   TestNativeDecideRememberRequiresCwd
--- PASS: TestNativeDecideRememberRequiresCwd (0.00s)
PASS
ok  	powercodedeck/ws	0.010s
```

**Run 2 — snapshot moved to AFTER Decide (sabotage), test FAILS:**
```
=== RUN   TestNativeDecideRememberSavesRule
    hub_native_decide_test.go:112: native:decide Remember=true 후 규칙이 저장되지 않았다 — 스냅샷이 Decide 이후에 찍히거나 Save 호출이 누락됐을 가능성이 있다
--- FAIL: TestNativeDecideRememberSavesRule (0.00s)
=== RUN   TestNativeDecideRememberRequiresCwd
--- PASS: TestNativeDecideRememberRequiresCwd (0.00s)
FAIL
FAIL	powercodedeck/ws	0.010s
```

**Run 3 — production code restored, test PASSES:**
```
=== RUN   TestNativeDecideRememberSavesRule
--- PASS: TestNativeDecideRememberSavesRule (0.00s)
=== RUN   TestNativeDecideRememberRequiresCwd
--- PASS: TestNativeDecideRememberRequiresCwd (0.00s)
PASS
ok  	powercodedeck/ws	0.010s
```

---

## Binary rebuild

`make build-windows` was run and `dist/pcd.exe` updated. Finding 1 changes client production code (useWebSocket.ts), so the embedded static assets in the binary changed.

## Lockfile churn

`client/package-lock.json` was touched by the `make build-windows` step (which runs `pnpm install` internally). This file had pre-existing uncommitted drift on the branch and was deliberately left unstaged per the task constraints.

---

## Full verification output

```
# Server:
cd server && CGO_ENABLED=0 go build ./...  # no output = clean
cd server && go test ./...
?   	powercodedeck	[no test files]
ok  	powercodedeck/auth	(cached)
ok  	powercodedeck/cli	(cached)
ok  	powercodedeck/config	(cached)
?   	powercodedeck/db	[no test files]
ok  	powercodedeck/handlers	0.142s
ok  	powercodedeck/middleware	(cached)
ok  	powercodedeck/services	2.215s
?   	powercodedeck/version	[no test files]
ok  	powercodedeck/ws	0.011s

# Client:
./node_modules/.bin/tsc --noEmit  # no output = clean
```

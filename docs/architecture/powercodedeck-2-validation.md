# Runtime extraction validation

Host: macOS arm64, Go 1.26.1. Baseline: `de39831` (VERSION 0.6.1).

## Baseline observations

- A fresh clone has no `server/static`, so `go test ./...` initially fails the root package's embed directive. Build the client and copy its dist first, as the existing Makefile expects.
- `pnpm install --frozen-lockfile` succeeds, but `pnpm build` fails because the client imports `@wterm/core` without declaring it directly. This is an existing dependency/lockfile issue; no frontend dependency files were changed. `npm ci --ignore-scripts --no-audit --no-fund` using the checked-in npm lockfile followed by `npm run build` passes. The two lockfiles resolve different versions (observed Vite 6.4.1 vs 6.4.3); standardizing them is separate work.
- Default macOS temp paths cause the pre-existing `TestIsSensitivePath` failure: the candidate is symlink-canonicalized but the fake home used by the test is not. Using `TMPDIR=/private/tmp` passes the baseline service, WebSocket, handler, auth, middleware, config, DB and CLI suites. This does not fix path canonicalization; track that as an existing security-path correctness issue. The tests themselves were not weakened or skipped.

## Final results

| Check | Result |
| --- | --- |
| Existing UI type check + Vite production build using npm lockfile | PASS; existing eval/chunk-size/mixed import warnings |
| `TMPDIR=/private/tmp go test -count=1 ./...` | PASS across all packages |
| `TMPDIR=/private/tmp go test -race -count=1 ./...` | PASS across all packages |
| macOS arm64 server build with actual embedded UI | PASS |
| Built binary `version` | `pcd v0.6.1` |
| Windows amd64, CGO disabled, full server cross-build | PASS |
| Linux amd64, CGO disabled, full server cross-build | PASS |
| `go list -deps ./internal/runtime/...` | Only the new session/runtime packages from this module; no services/handlers/ws/db imports |
| Launch helpers and terminal helper source comparison | Existing bodies preserved; package names and gofmt comment/spacing changes only |

## Regression coverage

Retained the legacy constructor integration tests for Create/Write/output/replay,
last-viewer detach, Kill/Restart, natural exit, ring capacity and restoring terminal
modes after buffer eviction. Existing locale, launch/bootstrap, HTTP and WebSocket
write-gate/native-identity/attach tests still run through the compatibility layer.

Moved flow-control, terminal-mode, terminal-query and split UTF-8 tests with their
private implementation. New independent runtime tests import no services package:

- A logical provider command resolves to a real shell through the launch preparer;
  argument boundaries, environment and working directory reach the process.
- Metadata retains the requested command and args, not the resolved shell.
- The session runs while detached, and replay catches subsequent output.
- Restart calls the preparer again and starts the next invocation.
- Missing preparer, preparer error and missing executable leave no registered
  session. Wrapped errors preserve `errors.Is`.

## Reproduce

From the repository root (Go toolchain and npm available):

```sh
cd client
npm ci --ignore-scripts --no-audit --no-fund
npm run build
mkdir -p ../server/static
cp -R dist/. ../server/static/
cd ../server
TMPDIR=/private/tmp go test -count=1 ./...
TMPDIR=/private/tmp go test -race -count=1 ./...
CGO_ENABLED=0 go build -o /tmp/pcd-runtime-refactor .
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/pcd-runtime-refactor.exe .
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/pcd-runtime-refactor-linux .
go list -deps ./internal/runtime/...
```

`TMPDIR=/private/tmp` is the macOS baseline workaround; use a real canonical temp
directory on other operating systems. Generated assets/binaries are not committed.
No deployed server or existing database was used or modified.

## Limits and next checks

Cross-builds prove compilation, not Windows ConPTY/Linux runtime behavior.
Browser-driven mobile handoff and real authenticated Claude/Codex live tests were
not run. Real-model tests remain opt-in and no model CLI was installed or invoked
for an agent turn. Before release, run platform PTY smoke tests and a browser
reconnect/handoff check. Before provider extraction, define provider-neutral events
and execution IDs, then test fake/recorded protocol streams and permission paths.

The runtime retains existing limitations: process lifetime is bound to the server,
one output callback needs explicit fan-out for multiple consumers, and callers must
serialize lifecycle operations for the same session ID (duplicate Create and
concurrent Restart/Kill are not made transactional by this extraction).

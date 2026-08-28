<p align="right"><a href="TROUBLESHOOTING.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Troubleshooting

Real gotchas already hit in this repo — most of them existed only as tacit knowledge of whoever
had touched deploy/tests before, not documented anywhere. Add anything new here that cost you more
than a few minutes to figure out.

## Deploy / Docker Compose

### `task db:up` or `task deploy:up` hanging/failing instead of just waiting for Postgres to come up

The tasks that wait for a service to become healthy use `docker compose up --wait`, not a manual
loop with `sleep`/`seq`. This is deliberate: `sleep` and `seq` aren't guaranteed to be on PATH on
plain Windows (outside Git Bash/WSL), so a hand-rolled polling loop breaks silently there. If
you're adding a new task that needs to wait on a container, prefer `--wait --wait-timeout N` (uses
the `healthcheck:` already declared in `docker-compose.yml`) instead of writing your own loop.

### `frontend` container healthcheck fails with `wget: bad address` or connection refused, but nginx "looks" like it's running

The official nginx image's entrypoint script that enables IPv6 (`20-envsubst-on-templates.sh` and
friends) only patches the image's *original, unmodified* `default.conf` — since
`frontend/nginx.conf` overwrites that file, the patch never runs, and nginx stays IPv4-only. The
catch is that the Alpine image's musl libc resolves `localhost` to `::1` (IPv6) first in
`/etc/hosts` — so `wget http://localhost/` from inside the container itself (that's how the
`docker-compose.yml` healthcheck tests it) tries IPv6 first, gets connection refused (nginx isn't
listening there), and fails.

Fix: `frontend/nginx.conf` declares `listen [::]:80;` explicitly, alongside the normal
`listen 80;` — manual dual-stack, since the image's auto-patch doesn't apply. `api`/`ingest` don't
have this problem because Go's `net.Listen(":PORT")` already binds dual-stack by default.

### A named Docker volume mounted empty gets `root` ownership, and the container process (running as a non-root user) can't write to it

Happens with any new volume mounted at a directory the image didn't previously have — Docker
creates the mount point as `root:root` the first time, even though the container runs as `USER
kuruops` (see `backend/Dockerfile`). Symptom: `permission denied` error trying to write there
right at container startup (this is how the `dev-jwt-keys:/app/.dev-keys` volume broke on the
first attempt).

Fix: create the destination directory *inside the Dockerfile*, as the non-root user, before
`ENTRYPOINT` (`RUN mkdir -p /app/.dev-keys` after the inherited `USER kuruops`) — Docker copies
that directory's ownership to the volume on first mount, since it already exists with the correct
owner in the image layer. If the volume was already created once with the wrong owner (because it
came up before the fix), rebuilding the image alone doesn't fix it — you need `docker volume rm`
on that specific volume to force ownership to be reinitialized.

## Tests

### A `nil` Go slice returned as JSON becomes `null`, not `[]`, and breaks frontend code that assumes an array

`json.Marshal` of a `[]T` that is `nil` (not `[]T{}`) produces the literal `null`. This is often
invisible in Go (`for range nil` doesn't panic, `len(nil)` is 0) but breaks any JS consumer that
calls `.map`/`.length` directly on the response assuming an array. It happens mainly when a value
comes from a `map[K][]V` and the key doesn't exist — `assignees[id]` on a map without that key
returns the slice's zero value, which is `nil`, not `[]V{}`.

Fix: normalize explicitly before serializing. See `orEmptyUserSummarySlice` in
`backend/internal/repository/incident_repository.go` as the pattern to copy — and cover it with a
test that fails if the normalization is removed (`incident_repository_test.go` comments on this
explicitly: "Must be [], not nil").

### The frontend test suite (Vitest) fails with `[vitest-pool-runner]: Timeout waiting for worker to respond`, but running it again passes

Symptom observed specifically on Windows running several heavy processes in parallel (e.g.,
`go test`, `golangci-lint`, and `vitest` at the same time) — real CPU contention, not a broken
test. `Test Files`/`Tests` still show as "passed" in the summary even with this error in the
middle of the log. Before investigating a specific test because of this, run the suite in
isolation (without other concurrent heavy processes) once — if it passes cleanly, it was
contention, not a bug.

### `go test` failing with `fatal error: out of memory allocating heap arena map`

Transient allocation failure of the Go runtime under system memory pressure (several JVMs/Docker
containers/Node processes running at the same time), not a bug in the code. Running it again
usually resolves it; if it persists, close heavy concurrent processes first.

### `secrets.EnvStore.Resolve` of a ref that never existed doesn't return an error

Contrary to what the `(string, error)` signature suggests, `EnvStore.Resolve` always returns a
`nil` error — an unknown ref simply resolves to the empty string `""`. A test expecting "resolving
an invalid ref should fail" will break in a non-obvious way (it's not the error that fails, it's
the next step that receives an empty credential). This is `EnvStore`-specific behavior —
real `VaultStore`/`AWSKMSStore` do return a real error in that case.

## Command-line tools in this environment (Windows/Git Bash)

### Killing a background process (Bash's `kill $PID`) doesn't actually bring the process down — it keeps holding the port

In Git Bash/MSYS, `$!` (the PID of the last background command) is a PID in MSYS space, which
doesn't necessarily correspond to the native Windows PID that `netstat`/Task Manager sees —
common when the background command is a wrapper that in turn spawns another process (e.g., `npx`
spawning a separate `node.exe`). `kill`/`pkill -P` on that PID doesn't reach the real process.

Reliable fix: find the real native PID via the port (`netstat -ano | grep ":PORT" | grep
LISTENING`, last column) and kill that one with `taskkill //F //PID <pid>` — don't rely on `$!`
for this scenario. See `backend/scripts/run-mcp-reference-test.sh`'s `stop_server` for a working
example of this pattern.

### A background process holding the same output (`stdout`) as the parent script hangs any `| tail` or other pipe consumer forever

If you start a background process (`command &`) without redirecting `stdout`/`stderr` to a file,
it inherits the parent script's file descriptor. If that whole script is being run as part of a
pipe (`task my-task | tail -N`), the pipe only closes (EOF) when *every* process holding that
descriptor open terminates — including the background process, even if the "main" script has
already logically finished and run its `trap ... EXIT`. Symptom: the command appears to hang
indefinitely with no output, even though the actual work (e.g., the tests) already ran and passed
a long time ago.

Fix: always redirect a background process's output to a file/`/dev/null` explicitly
(`command >"$LOG_FILE" 2>&1 &`), never let it inherit the parent script's stdout.

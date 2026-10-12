# binboat App Contract

The rules a web app must follow to be hosted on binboat, and everything needed to build one. Any language works as long as it produces a static Linux binary; this document has recipes for **Go**, **Zig** and **C**. Follow them and the app deploys with one click.

Contract version: **3** (the app's own GitHub Actions workflow builds it; binboat only deploys the result).

---

## 1. Summary checklist

- [ ] The repo has a `binboat.toml` at the root with `contract = 3` and `run`. The repo name becomes the app name (e.g. `my_blog` → `my-blog`).
- [ ] The repo has the release workflow (`.github/workflows/release.yml`, §2.2). Pushing a `v*` tag tests, builds and publishes a GitHub release.
- [ ] The release tarball holds everything the app needs at runtime: the binary and any templates or static files.
- [ ] The app serves plain HTTP on **`127.0.0.1:$PORT`**. No TLS.
- [ ] All SQLite files live in **`$DATA_DIR`**, and nothing else is written anywhere except `$DATA_DIR` and `$TMPDIR`.
- [ ] The app knows that binboat backs up **only its SQLite databases**; backing up any other file it keeps is the app's own job (§5).
- [ ] The binary is **static** (Go: `CGO_ENABLED=0`; Zig/C: a `musl` target), so it doesn't depend on the server's libc.
- [ ] `GET <health_path>` returns 2xx once the app is ready.
- [ ] On **SIGTERM**, the app finishes in-flight requests and exits within 10 s.
- [ ] Logs go to **stdout/stderr**, one line per entry, and stdout is line-buffered or flushed (§7). The app does not daemonize or fork into the background.
- [ ] Secrets come from environment variables, never from the repo.

---

## 2. Releases

### 2.1 `binboat.toml`

It says how to **run** the app, and it travels inside every release tarball. Operational settings (domain, env vars, resource limits) are set in the dashboard.

```toml
contract    = 3
run         = "zig-out/bin/myapp"   # required: binary + arguments, relative to the tarball root
health_path = "/healthz"            # optional, default "/"
```

| Field | Required | Default | Notes |
|---|---|---|---|
| `contract` | yes | — | `3`. |
| `run` | yes | — | Binary path plus arguments. It is **not run through a shell**, so pipes, `&&` and `$VAR` expansion don't work. The binary must be a Linux ELF executable for the server's CPU. |
| `health_path` | no | `/` | Must return 2xx or 3xx within 30 s of start. |

Unknown keys are rejected, so typos fail the deploy instead of being ignored.

### 2.2 The release workflow

Save this as `.github/workflows/release.yml` in the app's repo and edit the `env:` block at its top:

| Variable | Meaning | Zig | C (compiled with `zig cc`) |
|---|---|---|---|
| `ZIG_VERSION` | The Zig release that builds the app (C too) | `0.16.0` | `0.16.0` |
| `TEST` | Your tests; they run first, and a failure publishes nothing | `zig build test` | `make test CC="zig cc"` |
| `CLEAN` | Removes the previous architecture's build output | `rm -rf zig-out` | `rm -rf build` |
| `BUILD` | Builds for `$TARGET` (`x86_64-linux-musl`, then `aarch64-linux-musl`) | `zig build -Doptimize=ReleaseSafe -Dtarget=$TARGET` | `make CC="zig cc -target $TARGET"` |
| `FILES` | What goes into the tarball next to `binboat.toml` (paths kept) | `zig-out/bin/myapp templates static` | `build/myapp static` |

```yaml
# Tests, builds and publishes this app as a GitHub release that binboat can
# deploy. Release with:  git tag v1.0.0 && git push origin v1.0.0
# A failing test, build or smoke test publishes nothing. Nothing is deployed
# either: the release shows up in the app's Deploy menu in binboat.
name: release

on:
  push:
    tags: ["v*"]

permissions:
  contents: write # create the release

env:
  ZIG_VERSION: "0.16.0"
  TEST: zig build test                                  # C: make test CC="zig cc"
  CLEAN: rm -rf zig-out                                 # C: rm -rf build
  BUILD: zig build -Doptimize=ReleaseSafe -Dtarget=$TARGET   # C: make CC="zig cc -target $TARGET"
  FILES: zig-out/bin/myapp                              # C: build/myapp

jobs:
  release:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v7

      - name: Install Zig
        run: |
          set -euo pipefail
          if command -v zig >/dev/null && [ "$(zig version)" = "$ZIG_VERSION" ]; then exit 0; fi
          dir="${RUNNER_TEMP}/zig-${ZIG_VERSION}"
          if [ ! -x "$dir/zig" ]; then
            read -r url sum < <(curl -fsSL https://ziglang.org/download/index.json |
              python3 -c "import json,os,sys; e=json.load(sys.stdin)[os.environ['ZIG_VERSION']]['x86_64-linux']; print(e['tarball'], e['shasum'])")
            curl -fsSL --retry 3 -o "${RUNNER_TEMP}/zig.tar.xz" "$url"
            echo "$sum  ${RUNNER_TEMP}/zig.tar.xz" | sha256sum -c -
            mkdir -p "$dir" && tar -xJf "${RUNNER_TEMP}/zig.tar.xz" -C "$dir" --strip-components=1
          fi
          echo "$dir" >> "$GITHUB_PATH"

      - name: Test
        run: |
          eval "$CLEAN"
          eval "$TEST"

      - name: Build and package
        run: |
          set -euo pipefail
          rm -rf dist && mkdir dist
          for arch in amd64 arm64; do
            case "$arch" in
              amd64) export TARGET=x86_64-linux-musl ;;
              arm64) export TARGET=aarch64-linux-musl ;;
            esac
            eval "$CLEAN"
            eval "$BUILD"
            pkg="$(mktemp -d)" && chmod 755 "$pkg"
            cp binboat.toml "$pkg/"
            for f in $FILES; do
              mkdir -p "$pkg/$(dirname "$f")"
              cp -R "$f" "$pkg/$f"
            done
            tar -czf "dist/app-linux-$arch.tar.gz" -C "$pkg" .
            rm -rf "$pkg"
          done
          (cd dist && sha256sum app-linux-*.tar.gz > checksums.txt)
          cat dist/checksums.txt

      - name: Smoke test (start the amd64 package as binboat would)
        run: |
          set -euo pipefail
          python3 - <<'PY'
          import os, shlex, signal, socket, subprocess, sys, tarfile, tempfile, time, tomllib, urllib.request
          root = tempfile.mkdtemp()
          with tarfile.open("dist/app-linux-amd64.tar.gz") as t:
              t.extractall(root, filter="data")
          m = tomllib.load(open(os.path.join(root, "binboat.toml"), "rb"))
          assert m.get("contract") == 3, "binboat.toml: contract must be 3"
          argv = shlex.split(m["run"])
          s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
          data, tmp = tempfile.mkdtemp(), tempfile.mkdtemp()
          env = {"PATH": "/usr/bin:/bin", "HOST": "127.0.0.1", "PORT": str(port), "DATA_DIR": data, "TMPDIR": tmp,
                 "BINBOAT_APP": "smoke", "BINBOAT_RELEASE": os.environ.get("GITHUB_REF_NAME", "smoke"), "BINBOAT_CONTRACT": "3"}
          p = subprocess.Popen([os.path.join(root, argv[0])] + argv[1:], cwd=root, env=env)
          url = "http://127.0.0.1:%d%s" % (port, m.get("health_path", "/"))
          for _ in range(300):
              try:
                  if urllib.request.urlopen(url, timeout=1).status < 400:
                      break
              except Exception:
                  time.sleep(0.1)
          else:
              p.kill(); sys.exit("health check failed: " + url)
          p.send_signal(signal.SIGTERM)
          p.wait(timeout=10)
          print("healthy at", url, "and stopped on SIGTERM")
          PY

      - name: Publish the release
        env:
          GH_TOKEN: ${{ github.token }}
        run: gh release create "$GITHUB_REF_NAME" dist/* --title "$GITHUB_REF_NAME" --generate-notes --verify-tag
```

The workflow runs `TEST`, builds both architectures, packages `app-linux-amd64.tar.gz` and `app-linux-arm64.tar.gz`, writes `checksums.txt`, starts the amd64 package exactly as binboat would and waits for `health_path`, and only then publishes the release. Nothing is deployed yet: the release appears in the app's **Deploy** menu in binboat, and you pick when.

- Tags must match `v*` and use only letters, digits, `.`, `_` and `-` (e.g. `v1.4.0`, `v1.4.0-rc1`); the tag becomes `BINBOAT_RELEASE`.
- Push the workflow file in a commit **before** the first tag: a tag pushed together with the repository's first commit does not start the workflow.
- Builds run on GitHub's runners, never on your server. Private repos work: binboat downloads assets with the GitHub token from its settings (*Contents: Read-only*).
- The tarball is unpacked read-only (owned by root). Anything the app writes goes in `$DATA_DIR` or `$TMPDIR`.

### 2.3 SQLite in the build

`musl` targets give static binaries that don't depend on the server's libc, so SQLite is compiled in from source. (Go apps: see §2.4.)

**C** (`Makefile`): download the amalgamation once, verify it, compile it with the app. `CC` comes from the workflow.

```make
SQLITE_VER := 3530400
SQLITE_TGZ := sqlite-autoconf-$(SQLITE_VER).tar.gz
CFLAGS += -O2 -std=c11 -Wall -D_GNU_SOURCE -Ithird_party

build/myapp: src/main.c build/sqlite3.o
	mkdir -p build && $(CC) $(CFLAGS) -o $@ src/main.c build/sqlite3.o -lm
build/sqlite3.o: third_party/sqlite3.c
	mkdir -p build && $(CC) -O2 -DSQLITE_THREADSAFE=0 -DSQLITE_OMIT_LOAD_EXTENSION -c -o $@ $<
third_party/sqlite3.c:
	mkdir -p third_party
	curl -fsSL -o third_party/$(SQLITE_TGZ) https://sqlite.org/2026/$(SQLITE_TGZ)
	echo "<sha256 of the tarball>  third_party/$(SQLITE_TGZ)" | sha256sum -c -
	tar -xzf third_party/$(SQLITE_TGZ) -C third_party --strip-components=1 \
		sqlite-autoconf-$(SQLITE_VER)/sqlite3.c sqlite-autoconf-$(SQLITE_VER)/sqlite3.h
test: ...   # build and run your tests
```

**Zig** (0.16): add the amalgamation zip as a dependency with `zig fetch --save https://sqlite.org/2026/sqlite-amalgamation-3530400.zip`, then in `build.zig`:

```zig
const sqlite = b.dependency("sqlite", .{});
const c = b.addTranslateC(.{ .root_source_file = sqlite.path("sqlite3.h"), .target = target, .optimize = optimize });
const mod = b.createModule(.{
    .root_source_file = b.path("src/main.zig"), .target = target, .optimize = optimize, .link_libc = true,
    .imports = &.{.{ .name = "c", .module = c.createModule() }},   // @import("c") in main.zig
});
mod.addCSourceFile(.{ .file = sqlite.path("sqlite3.c"), .flags = &.{ "-DSQLITE_THREADSAFE=0", "-DSQLITE_OMIT_LOAD_EXTENSION" } });
b.installArtifact(b.addExecutable(.{ .name = "myapp", .root_module = mod }));
const tests = b.addTest(.{ .root_module = mod });
b.step("test", "Run the tests").dependOn(&b.addRunArtifact(tests).step);
```

### 2.4 Go apps

A Go app needs no toolchain beyond Go itself. Build with `CGO_ENABLED=0` so the binary is static, and use a **pure-Go** SQLite driver such as [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite). Drivers that need cgo (like `mattn/go-sqlite3`) produce a binary that depends on the build machine's libc, which binboat can't guarantee on the server.

`binboat.toml` for an app whose binary is called `myapp`:

```toml
contract    = 3
run         = "myapp"
health_path = "/healthz"
```

Use this as `.github/workflows/release.yml` instead of the workflow in §2.2. Edit `BIN` (the binary name, which must match `run`) and `EXTRA` (other files or folders the app reads at runtime, such as `templates static`; may be empty). The Go version comes from your `go.mod`.

```yaml
# Tests, builds and publishes this app as a GitHub release that binboat can
# deploy. Release with:  git tag v1.0.0 && git push origin v1.0.0
# A failing test, build or smoke test publishes nothing. Nothing is deployed
# either: the release shows up in the app's Deploy menu in binboat.
name: release

on:
  push:
    tags: ["v*"]

permissions:
  contents: write # create the release

env:
  BIN: myapp
  EXTRA: "templates static"

jobs:
  release:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v7

      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod

      - name: Test
        run: go test ./...

      - name: Build and package
        run: |
          set -euo pipefail
          rm -rf dist && mkdir dist
          for arch in amd64 arm64; do
            pkg="$(mktemp -d)" && chmod 755 "$pkg"
            CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "-s -w" -o "$pkg/$BIN" .
            cp binboat.toml "$pkg/"
            for f in $EXTRA; do
              mkdir -p "$pkg/$(dirname "$f")"
              cp -R "$f" "$pkg/$f"
            done
            tar -czf "dist/app-linux-$arch.tar.gz" -C "$pkg" .
            rm -rf "$pkg"
          done
          (cd dist && sha256sum app-linux-*.tar.gz > checksums.txt)
          cat dist/checksums.txt

      - name: Smoke test (start the amd64 package as binboat would)
        run: |
          set -euo pipefail
          python3 - <<'PY'
          import os, shlex, signal, socket, subprocess, sys, tarfile, tempfile, time, tomllib, urllib.request
          root = tempfile.mkdtemp()
          with tarfile.open("dist/app-linux-amd64.tar.gz") as t:
              t.extractall(root, filter="data")
          m = tomllib.load(open(os.path.join(root, "binboat.toml"), "rb"))
          assert m.get("contract") == 3, "binboat.toml: contract must be 3"
          argv = shlex.split(m["run"])
          s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
          data, tmp = tempfile.mkdtemp(), tempfile.mkdtemp()
          env = {"PATH": "/usr/bin:/bin", "HOST": "127.0.0.1", "PORT": str(port), "DATA_DIR": data, "TMPDIR": tmp,
                 "BINBOAT_APP": "smoke", "BINBOAT_RELEASE": os.environ.get("GITHUB_REF_NAME", "smoke"), "BINBOAT_CONTRACT": "3"}
          p = subprocess.Popen([os.path.join(root, argv[0])] + argv[1:], cwd=root, env=env)
          url = "http://127.0.0.1:%d%s" % (port, m.get("health_path", "/"))
          for _ in range(300):
              try:
                  if urllib.request.urlopen(url, timeout=1).status < 400:
                      break
              except Exception:
                  time.sleep(0.1)
          else:
              p.kill(); sys.exit("health check failed: " + url)
          p.send_signal(signal.SIGTERM)
          p.wait(timeout=10)
          print("healthy at", url, "and stopped on SIGTERM")
          PY

      - name: Publish the release
        env:
          GH_TOKEN: ${{ github.token }}
        run: gh release create "$GITHUB_REF_NAME" dist/* --title "$GITHUB_REF_NAME" --generate-notes --verify-tag
```

It behaves like the workflow in §2.2: tests first, both architectures, checksums, a smoke test that starts the package as binboat would, and only then the release. The rest of §2.2 (tag rules, pushing the workflow before the first tag, private repos) applies unchanged.

**Memory.** binboat sets `GOMEMLIMIT` to 90% of the app's memory limit (unless you set `GOMEMLIMIT` yourself in the dashboard), because the Go runtime doesn't read cgroup limits. The garbage collector then works harder as the app nears its limit instead of letting the kernel kill it. `GOMAXPROCS` follows the app's CPU limit automatically with Go 1.25 and newer.

---

## 3. Environment variables binboat provides

| Variable | Example | Meaning |
|---|---|---|
| `PORT` | `20001` | The port to listen on. It stays the same for an app across deploys. |
| `HOST` | `127.0.0.1` | The address to bind to. Always bind here, never to `0.0.0.0`. |
| `DATA_DIR` | `/var/lib/binboat/apps/blog/data` | Persistent and survives every deploy. Its SQLite files are backed up; nothing else in it is (§5). |
| `TMPDIR` | `/var/lib/binboat/apps/blog/tmp` | Scratch space that may be wiped on every deploy. |
| `BINBOAT_APP` | `blog` | The app's name (from the repository name). |
| `BINBOAT_DOMAIN` | `blog.example.com` | The public domain. |
| `BINBOAT_RELEASE` | `v1.4.0` | The deployed release (its tag). |
| `BINBOAT_CONTRACT` | `3` | The contract version. |

binboat also sets `PATH` and `LANG`, and adds your own env vars from the dashboard; nothing else is inherited (no `HOME`). The one extra: `GOMEMLIMIT` is set to 90% of the memory limit unless you define it yourself (§2.4); other languages ignore it. Names starting with `BINBOAT_`, and the names in the table above, are reserved. Changing env vars requires a **Restart**, not a new release.

---

## 4. HTTP rules

- Serve **plain HTTP/1.1**. binboat handles TLS in front of your app.
- Headers you can rely on:
  - `Host`: your domain.
  - `X-Forwarded-Proto: https` and `X-Forwarded-Host` (your domain).
  - `X-Forwarded-For` and `X-Real-IP`: the **real visitor IP**, taken from Cloudflare's `CF-Connecting-IP`. Whatever the visitor sent in these headers is replaced, so they are safe to trust.
  - `CF-Connecting-IP` and `CF-IPCountry`, passed through from Cloudflare.
- WebSockets, long-polling and streamed responses (SSE) work; responses are flushed to the visitor immediately.
- Cloudflare limits request bodies to 100 MB on the free plan, so keep uploads smaller than that.
- While a deploy is running, visitors see a short binboat "deploying" 503 page. Expect well under a second of downtime, plus however long your app takes to become healthy.
- While your app is stopped or crashed, visitors get a binboat 503 page; if it stops answering unexpectedly, a 502.

---

## 5. Filesystem rules

| Path | Access | Survives deploy? | Backed up? |
|---|---|---|---|
| Release dir (the unpacked tarball, current working dir) | read-only | replaced | no |
| `$DATA_DIR` | read/write | ✅ yes | ✅ SQLite files |
| `$TMPDIR` | read/write | ❌ may be wiped | no |
| Other apps' folders, binboat's files | no access | — | — |

- Static files and templates in the tarball can be read with relative paths, because the working directory is the release dir. List them in `FILES` (§2.2).
- **Only SQLite databases in `$DATA_DIR` are backed up** (sub-folders included). binboat finds them by their file header, whatever their name.
- **Everything else in `$DATA_DIR` is yours.** Uploads, caches, generated files and so on are kept across deploys, restarts and rollbacks, but binboat does not back them up, copy them to the bucket or restore them. They are not part of a **Restore**, and they are lost if the server is lost. An app that needs those files protected must do it itself, for example by storing them in an object store or by exporting them on a schedule. This is a deliberate boundary, not a missing feature: binboat backs up its apps' databases and nothing more.
- Data an app can't afford to lose belongs in SQLite. Small amounts of state (settings, counters, sessions, even small blobs) fit there and get backed up with everything else.
- Don't rely on `$TMPDIR` for anything you need later; it is emptied on every deploy.
- Don't create folders named `.pre-restore-*` in `$DATA_DIR`: binboat uses them to keep the previous databases after a restore.

---

## 6. SQLite rules

1. Open databases **only** from `$DATA_DIR`, for example `$DATA_DIR/app.db`.
2. Recommended settings on every connection:
   ```sql
   PRAGMA journal_mode = WAL;
   PRAGMA busy_timeout = 5000;
   PRAGMA foreign_keys = ON;
   PRAGMA synchronous = NORMAL;
   ```
   Backups read the database while your app is running. `busy_timeout` makes sure a backup never causes a "database is locked" error in your app.
3. **Run migrations at startup**, before you start listening. binboat takes a backup right before every deploy, so a bad migration can be undone with **Restore** in the dashboard.
4. Migrations should only move forward, and ideally stay compatible with the previous release. Rolling back the code does **not** roll back the database.

---

## 7. Lifecycle

```
start ──► app runs migrations ──► listens on HOST:PORT ──► health_path returns 2xx ──► receives traffic
                                                                                           │
SIGTERM (deploy / restart / stop) ──► finish requests, close DB ──► exit 0 ◄──────────────┘
                                  └─► after 10 s: SIGKILL
```

- binboat starts your binary directly and supervises it. If it crashes, binboat restarts it with backoff (1 s, 2 s, 4 s … up to 60 s). After 5 crashes in 5 minutes, the app is marked **crashed** and left stopped.
- Don't daemonize, double-fork or write PID files.
- When the main process exits, binboat kills anything else still running in the app's cgroup.
- stdout is a pipe, not a terminal, so C's stdio buffers it fully by default. Call `setvbuf(stdout, NULL, _IOLBF, 0)` at startup (or write logs to stderr), otherwise log lines appear late or are lost on a crash. `std.debug.print` in Zig writes unbuffered to stderr, and so does Go's `log` package.
- Lines longer than 16 KB are split into several log lines.

---

## 8. Resource limits (defaults, editable per app)

| Limit | Default |
|---|---|
| Memory (`memory.max`) | 256 MB, no swap. The app is OOM-killed and restarted if it goes above this. Go apps also get `GOMEMLIMIT` at 90% of it (§2.4). |
| CPU (`cpu.max`) | 1 core |
| Processes/threads (`pids.max`) | 256 |
| Open files | 4096 |

---

## 9. Reading the contract in code

### C

```c
#include <stdlib.h>

const char *host     = getenv("HOST");      // "127.0.0.1"
int         port     = atoi(getenv("PORT"));
const char *data_dir = getenv("DATA_DIR");
// db path: snprintf(path, sizeof path, "%s/app.db", data_dir);
```

Handle SIGTERM:

```c
#include <signal.h>
static volatile sig_atomic_t stop = 0;
static void on_term(int s) { (void)s; stop = 1; }
// in main: signal(SIGTERM, on_term);  then have your accept loop check `stop`
// (e.g. poll() the listening socket with a 500 ms timeout)
```

### Go

```go
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	_ "modernc.org/sqlite" // pure Go, no cgo
)

func main() {
	dsn := "file:" + filepath.Join(os.Getenv("DATA_DIR"), "app.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err == nil {
		err = db.Ping() // opens the file; fails fast on a bad DATA_DIR
	}
	if err != nil {
		log.Fatal(err)
	}
	// run migrations here, before listening

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	srv := &http.Server{Addr: os.Getenv("HOST") + ":" + os.Getenv("PORT"), Handler: mux}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 8*time.Second) // binboat kills after 10 s
		defer cancel()
		srv.Shutdown(sctx)
		close(done)
	}()
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-done
	db.Close()
}
```

### Zig

The std API changes between Zig versions, so check the docs for **your `ZIG_VERSION`**. In 0.16 `main` receives the environment:

```zig
pub fn main(init: std.process.Init) !void {
    const env = init.environ_map;
    const port = try std.fmt.parseInt(u16, env.get("PORT").?, 10);
    const data_dir = env.get("DATA_DIR").?;
    // ...
}
```

---

## 10. Minimal repo layout example

```
myapp/
├── .github/workflows/release.yml   # §2.2
├── binboat.toml
├── build.zig                       # or a Makefile for C (§2.3), or go.mod for Go (§2.4)
├── build.zig.zon
├── src/main.zig
├── templates/     # in FILES; read at runtime via relative path
└── static/
```

A Go app has `go.mod`, `main.go`, `binboat.toml` and the workflow from §2.4 instead of the Zig or C build files; `run` is just the binary name.

# sumimi-gotest

The measuring instrument for [Sumimi](https://github.com/sumimi-dev/sumimi) — a
**load generator** and a **probe**, written in Go.

* **One static file, no dependencies.** It runs on the machine being measured,
  or on another one over the network, with nothing installed but the server and
  this binary. The standard library only: no modules to download, no runtime to
  install, no toolchain on the target.
* **It reports the machine.** Every report says which machine was measured —
  not which machine measured. A number without its machine is not a
  measurement.

## The three commands

```bash
# What does the server say about itself?
sumimi-gotest probe --host 127.0.0.1 --port 3000

# Send traffic at a rate, and count what came back.
sumimi-gotest load --host 127.0.0.1 --port 3000 --rate 20000 --seconds 20

# The matrix: a rate curve, written as a report.
sumimi-gotest sweep --host 127.0.0.1 --port 3000 \
    --rates 5000,10000,20000,40000 --seconds 20 --out report.md
```

The password comes from `SUMIMI_ADMIN_PASSWORD` when `--password` is not
given.

## Why Go, and not Rust

The probe is deliberately a **second implementation**: it speaks HTTP, not the
server's types. A client that shares code with the server cannot disagree with
it — and a client that cannot disagree cannot find anything. When the server
renames a field or changes a format, a Rust client built on the server's own
models would follow it silently; this one does not.

It is also the language that can be **sent anywhere**: `go build` produces a
static binary for every target in seconds, with no cross-compiler and no
Docker. The machine being measured is usually a plain VPS with no toolchain
on it.

The one thing Go gives up — the garbage collector — is turned off during a
run, because a collection pause would disturb the offered rate, and the
offered rate is the thing being measured.

## What the numbers mean

| the number | what it is |
| --- | --- |
| the rate offered | what the tool asked for, in requests a second |
| the rate achieved | what it actually managed — if the two differ, the machine was too busy |
| accepted | the server took it (`200`) |
| refused | the server said no (`503`) — the admission gate |
| the server's peak | the **high-water mark** of the server's resident set |

The peak is the number that matters for a memory budget: it is the exact
high-water mark from `/proc`, the same number the memory budgets are asserted
against. The server reports it itself (`/api/heartbeat`), so the tool does not
need to run on the same machine as the server.

**The peak only goes up.** So the `sweep` starts a **fresh** server for every
point of the curve when it is given a `--binary`; without one it measures a
running server and says so in the report.

## The report

```markdown
# The measurement — 2026-11-08 14:03 UTC

**The machine that was measured**: Intel Xeon 6975P-C · 2 cores · 7.3 GB · linux
**The machine that measured it**: Apple M5 · 8 cores · 16 GB · darwin
**The server**: Sumimi 0.4.0

| the rate offered | sent | accepted | refused | the rate achieved | the server's peak |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 5 000 | 100 000 | 100 000 | 0 | 5 000 | 84 MB |
| 20 000 | 400 000 | 390 000 | 10 000 | 19 998 | 253 MB |
```

`--json` writes the same thing for a machine to read.

## Building it

```bash
go build ./cmd/sumimi-gotest
go test ./...
```

The release workflow builds the six platforms — macOS, Linux and Windows, each
in arm64 and x64 — and attaches them to the release.

## Licence

Apache-2.0, the same as Sumimi.

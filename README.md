# toomanypings: find gRPC keepalive settings that end in GOAWAY "too_many_pings"

[![CI](https://github.com/GillMacedo/toomanypings/actions/workflows/ci.yml/badge.svg)](https://github.com/GillMacedo/toomanypings/actions/workflows/ci.yml) [![MIT license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**A Claude Code skill and Go CLI that reads grpc-go source and finds clients whose keepalive pings the server will punish with `GOAWAY ENHANCE_YOUR_CALM "too_many_pings"`, before the connection drops in production.**

A grpc-go client with `keepalive.ClientParameters{Time: 10 * time.Second}` pings the server whenever an RPC is quiet for 10 seconds. A server that sets no `keepalive.EnforcementPolicy` accepts one ping every 5 minutes, and after three early pings it closes the connection. The RPC fails with `code = Unavailable ... received prior goaway: code: ENHANCE_YOUR_CALM, debug data: "too_many_pings"`. Nothing fails at build time or in short tests: it only happens on a long call or a quiet stream, about four pings in.

`toomanypings` finds every client keepalive setting and every server enforcement policy in your Go code, works out the values, and compares them the way grpc-go's transport does.

## What you can ask Claude

- "Our watch stream fails with ENHANCE_YOUR_CALM too_many_pings after a minute. Which setting is wrong?"
- "Check that our gRPC clients' keepalive settings match what our servers allow."
- "We want to detect dead connections faster. What keepalive Time can our client use with this server?"
- "Does this client's PermitWithoutStream setting get idle connections closed?"

## Install

As a Claude Code plugin:

    claude plugin marketplace add GillMacedo/toomanypings
    claude plugin install toomanypings@toomanypings-marketplace

The skill builds the tool from source with Go 1.22 or newer. It has no dependencies. To run it yourself, clone the repo and use `go run ./plugin/scripts`, or `sh plugin/scripts/run.sh` from any folder.

## Example

`examples/mismatch` is a small inventory service. The server sets `MaxConnectionIdle` but no enforcement policy. The client pings every 15 seconds, also when no RPC is running, to notice dead connections on its watch stream:

    $ go run ./plugin/scripts --fail-on never examples/mismatch
    toomanypings: 1 client keepalive setting checked against 1 server policy

    ERROR KA101 examples/mismatch/client/main.go:19  Client pings every 15s during a quiet RPC, and the server allows one ping every 5m
      Client: grpc.WithKeepaliveParams at examples/mismatch/client/main.go:19, Time 15s, PermitWithoutStream true
      Server: grpc.NewServer at examples/mismatch/server/main.go:20 with no keepalive.EnforcementPolicy: MinTime 5m (default), PermitWithoutStream false
      When an RPC gets no data from the server for 15s, such as a long unary call or a quiet stream, the client pings. The server counts each ping that comes less than MinTime after the last one, and after 3 such pings it sends GOAWAY ENHANCE_YOUR_CALM "too_many_pings". In-flight RPCs on the connection fail with code Unavailable, about 1m into the quiet period.
      grpc-go then doubles Time once, to 30s, for later connections on that channel. That is still below 5m, so it keeps happening.
      Fix: Raise the client's Time to 5m or more, or set keepalive.EnforcementPolicy{MinTime: 15 * time.Second} or lower on the server.

    WARN KA102 examples/mismatch/client/main.go:19  Client pings idle connections every 15s, and the server closes them
      Client: grpc.WithKeepaliveParams at examples/mismatch/client/main.go:19, Time 15s, PermitWithoutStream true
      Server: grpc.NewServer at examples/mismatch/server/main.go:20 with no keepalive.EnforcementPolicy: MinTime 5m (default), PermitWithoutStream false
      With PermitWithoutStream true the client also pings when no RPC is running. A server with PermitWithoutStream false counts every such ping that comes within 2h of the last one, and one with PermitWithoutStream true counts pings that come sooner than MinTime. After about 1m idle, the server sends GOAWAY ENHANCE_YOUR_CALM "too_many_pings" and closes the connection. The client reconnects, and an RPC that starts at that moment can fail with code Unavailable.
      Fix: Set keepalive.EnforcementPolicy{MinTime: 15 * time.Second, PermitWithoutStream: true} on the server, or PermitWithoutStream: false on the client.

    Result: 2 findings where keepalive pings get connections closed.
    toomanypings reads grpc-go source only. Values from config files and flags, other gRPC languages, and proxies between client and server aren't seen.

`--fail-on never` is here only so the README's example exits 0. Without it, findings exit 1.

`examples/healthy` is the same pair, with `keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}` on the server:

    $ go run ./plugin/scripts examples/healthy
    toomanypings: 1 client keepalive setting checked against 1 server policy

    Result: every client keepalive setting is accepted by the server policies it was compared with.
    toomanypings reads grpc-go source only. Values from config files and flags, other gRPC languages, and proxies between client and server aren't seen.

### On real code

`tests/testdata/sonic-gnmi-before` holds four files from [sonic-net/sonic-gnmi](https://github.com/sonic-net/sonic-gnmi) at commit 181841b, before its fix for issue 619. That issue reports `too_many_pings` disconnections during `SetPackage` calls that run for minutes, between the DPU proxy client and the gNMI server:

    $ go run ./plugin/scripts --fail-on never tests/testdata/sonic-gnmi-before
    toomanypings: 1 client keepalive setting checked against 4 server policies

    ERROR KA101 tests/testdata/sonic-gnmi-before/pkg/interceptors/dpuproxy/proxy.go:184  Client pings every 10s during a quiet RPC, more often than any of the 4 servers in these files allows
      Client: grpc.WithKeepaliveParams at tests/testdata/sonic-gnmi-before/pkg/interceptors/dpuproxy/proxy.go:184, Time 10s, PermitWithoutStream true
      Server: grpc.NewServer at tests/testdata/sonic-gnmi-before/dialout/dialout_server/dialout_server.go:49 with no keepalive.EnforcementPolicy: MinTime 5m (default), PermitWithoutStream false
      Server: grpc.NewServer at tests/testdata/sonic-gnmi-before/gnmi_server/server.go:509 with no keepalive.EnforcementPolicy: MinTime 5m (default), PermitWithoutStream false
      Server: grpc.NewServer at tests/testdata/sonic-gnmi-before/gnmi_server/server.go:552 with no keepalive.EnforcementPolicy: MinTime 5m (default), PermitWithoutStream false
      Server: grpc.NewServer at tests/testdata/sonic-gnmi-before/gnmi_server/server.go:566 with no keepalive.EnforcementPolicy: MinTime 5m (default), PermitWithoutStream false
      When an RPC gets no data from the server for 10s, such as a long unary call or a quiet stream, the client pings. The server counts each ping that comes less than MinTime after the last one, and after 3 such pings it sends GOAWAY ENHANCE_YOUR_CALM "too_many_pings". In-flight RPCs on the connection fail with code Unavailable, about 40s into the quiet period.
      grpc-go then doubles Time once, to 20s, for later connections on that channel. That is still below 5m, so it keeps happening.
      Fix: Raise the client's Time to 5m or more, or set keepalive.EnforcementPolicy{MinTime: 10 * time.Second} or lower on the server.

    WARN KA102 tests/testdata/sonic-gnmi-before/pkg/interceptors/dpuproxy/proxy.go:184  Client pings idle connections every 10s, and the server closes them
      Client: grpc.WithKeepaliveParams at tests/testdata/sonic-gnmi-before/pkg/interceptors/dpuproxy/proxy.go:184, Time 10s, PermitWithoutStream true
      Server: grpc.NewServer at tests/testdata/sonic-gnmi-before/dialout/dialout_server/dialout_server.go:49 with no keepalive.EnforcementPolicy: MinTime 5m (default), PermitWithoutStream false
      Server: grpc.NewServer at tests/testdata/sonic-gnmi-before/gnmi_server/server.go:509 with no keepalive.EnforcementPolicy: MinTime 5m (default), PermitWithoutStream false
      Server: grpc.NewServer at tests/testdata/sonic-gnmi-before/gnmi_server/server.go:552 with no keepalive.EnforcementPolicy: MinTime 5m (default), PermitWithoutStream false
      Server: grpc.NewServer at tests/testdata/sonic-gnmi-before/gnmi_server/server.go:566 with no keepalive.EnforcementPolicy: MinTime 5m (default), PermitWithoutStream false
      With PermitWithoutStream true the client also pings when no RPC is running. A server with PermitWithoutStream false counts every such ping that comes within 2h of the last one, and one with PermitWithoutStream true counts pings that come sooner than MinTime. After about 40s idle, the server sends GOAWAY ENHANCE_YOUR_CALM "too_many_pings" and closes the connection. The client reconnects, and an RPC that starts at that moment can fail with code Unavailable.
      Fix: Set keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true} on the server, or PermitWithoutStream: false on the client.

    Result: 2 findings where keepalive pings get connections closed.
    toomanypings reads grpc-go source only. Values from config files and flags, other gRPC languages, and proxies between client and server aren't seen.

The fix (PR 634, commit 1bd6083) raised the client's Time to 30s and gave the server `MinTime: 20 * time.Second, PermitWithoutStream: true`. The same files after that commit:

    $ go run ./plugin/scripts tests/testdata/sonic-gnmi-after
    toomanypings: 1 client keepalive setting checked against 1 server policy

    Result: every client keepalive setting is accepted by the server policies it was compared with.
    toomanypings reads grpc-go source only. Values from config files and flags, other gRPC languages, and proxies between client and server aren't seen.

### What grpc-go really does

`examples/repro` runs a real grpc-go v1.85.0 server and client in one process, with a handler that stays silent for as long as you ask. `make repro` runs it; it downloads grpc-go, so it needs network access and Go 1.26 or newer. These lines were recorded on 2026-11-24:

| Client | Server | Result |
|---|---|---|
| Time 10s | no policy | `rpc after 41s: err=rpc error: code = Unavailable desc = closing transport due to: connection error: desc = "error reading from server: EOF", received prior goaway: code: ENHANCE_YOUR_CALM, debug data: "too_many_pings"` |
| Time 10s | MinTime 5s, or 10s | `rpc after 1m10s: err=<nil>` |
| Time 30s | MinTime 20s | `rpc after 2m10s: err=<nil>` (a 130s call) |
| Time 10s | no policy, `ServerParameters{Time: 2s}` | `rpc after 1m10s: err=<nil>`, because the server's pings count as activity and the client never pings |
| Time 10s | no policy, `ServerParameters{Time: 15s}` | GOAWAY after 41s |
| Time 10s, PermitWithoutStream, no RPC | no policy | GOAWAY at 40s, reconnect, GOAWAY again at 2m0s: grpc-go doubles Time once, to 20s, which is still too often |
| Time 10s, PermitWithoutStream, no RPC | PermitWithoutStream true, MinTime not set | GOAWAY at 40s: MinTime 0 means the 5m default |
| Time 10s, PermitWithoutStream, no RPC | PermitWithoutStream true, MinTime 5s | no GOAWAY in 70s |

## Usage

    toomanypings [--format text|json] [--fail-on warn|error|never] [--tests]
                 [--server-min-time DURATION] [--server-permit-without-stream]
                 PATH...

- `PATH` is a Go file or a folder, read recursively. Pass the code of the clients and of the servers they call. `vendor`, `testdata`, hidden folders and `_test.go` files are skipped. `--tests` includes test files.
- When no server is in the input, clients are compared with gRPC's default server policy (MinTime 5m, no pings without RPCs), which grpc-go, grpc-java and gRPC C-core share. If the server is elsewhere and sets its own policy, describe it with `--server-min-time 20s --server-permit-without-stream`.
- `--format json` prints the same findings for scripts and agents.

| Exit code | Meaning |
|---|---|
| 0 | No finding at the `--fail-on` level, or `--fail-on never` |
| 1 | Findings at the `--fail-on` level: `warn` by default |
| 2 | Bad options |
| 3 | The input can't be trusted, or there is nothing to compare. No result is printed, whatever `--fail-on` says |

Exit 3 covers missing paths, files that aren't `.go`, folders with no Go files, empty files, binary or random data, Go that doesn't parse (such as a file cut off partway), files that start like the output of a failed `go build`, `go test`, `make` or panic, input with no file that imports grpc, and input with no gRPC client at all. `go run` turns any failing exit code into 1, so build the binary when you need the exact code:

    ./toomanypings examples/broken examples/healthy/server
    toomanypings: can't trust this input, so there is no result.
    examples/broken/client.go: starts like the output of a failed command, not Go source: "# github.com/example/inventory/client"

## Findings

| Code | Level | Meaning |
|---|---|---|
| KA101 | error | The client pings during a quiet RPC more often than the server's MinTime allows, so the server sends GOAWAY and in-flight RPCs fail with Unavailable. A warning when the server was assumed or some server policies couldn't be read |
| KA102 | warn | The client pings idle connections (`PermitWithoutStream: true`) and the server doesn't allow it, so idle connections are closed and reopened |
| KA103 | note | The client's pings are too frequent for some servers in the input but not others. The source doesn't show which one it dials. Doesn't fail |
| KA301 | note | Time is below 10s or not set, so grpc-go pings every 10s |
| KA901 | warn | A client keepalive value comes from configuration, a flag or a parameter, so it wasn't checked |
| KA902 | warn | A server's MinTime, PermitWithoutStream or ping interval can't be read, so clients weren't compared with that server |

How the comparison works, all from grpc-go v1.85.0's source:

- The client's effective Time is at least 10s (`WithKeepaliveParams`). A server's MinTime of 0 means 5 minutes.
- During an RPC the server counts a strike for each ping that comes less than MinTime after the previous one, and sends GOAWAY when strikes pass 2. Strikes reset when the server sends headers or data, so the problem shows on calls and streams where the server is silent.
- On a connection with no RPC, a server with `PermitWithoutStream: false` counts any ping within 2 hours of the last one.
- A client only pings after Time with nothing received, so a server whose `ServerParameters.Time` is shorter than the client's Time keeps the client from pinging at all.
- After a `too_many_pings` GOAWAY, grpc-go sets the channel's Time to twice the configured value, once.

## How it compares

The keepalive docs of [grpc-go](https://github.com/grpc/grpc-go/blob/master/Documentation/keepalive.md) and [gRPC core](https://github.com/grpc/grpc/blob/master/doc/keepalive.md) explain the rule, and grpc-go logs the GOAWAY when it happens. Go linters such as `go vet`, staticcheck and golangci-lint don't compare a client's keepalive with a server's policy, and GitHub searches on 2026-11-24 for keepalive linters or checkers found none. `toomanypings` is a static check on source, so it runs in pull request CI.

## Limits

- **grpc-go only.** Clients and servers in other languages, and settings in config files, environment variables or flags, aren't read. Values computed at run time are reported as KA901 or KA902 instead of being guessed.
- **Which client talks to which server isn't in the source.** A client is an error only when every server policy in the input rejects it. When only some do, it is a note (KA103). Pass the folders of one client and the server it calls to get a direct answer.
- **A policy applies to its package.** A `grpc.NewServer` call is treated as running on defaults when its package sets no policy and its options are built there, or when nothing in the input sets a policy. A package with several servers and one policy is treated as if every server had it.
- **Only literals are followed**, through a local variable, a package-level constant or variable, or a constant in another package of the same module. A variable whose fields are changed after the literal, such as `kp.Time = d`, is reported as unreadable (KA901 or KA902).
- **Proxies.** HTTP/2 pings are between neighbors. With Envoy, a service mesh sidecar or an L7 load balancer in between, the client's pings reach the proxy, not the server.
- **A file cut between two complete declarations** still parses, so it can't be detected. A cut that removes the client leaves nothing to compare (exit 3), and one that removes the server's policy produces a finding, so no cut of the examples prints a clean result. A cut elsewhere in a real file can.
- **Timings** in messages (about 4 × Time to the GOAWAY) come from the strike count and match the recordings above, but depend on traffic: any data the server sends resets the count.

## FAQ

**What does "ENHANCE_YOUR_CALM" with debug data "too_many_pings" mean?** The server closed the connection because the client sent HTTP/2 keepalive pings more often than the server's enforcement policy allows. In grpc-go that is `keepalive.EnforcementPolicy.MinTime`, 5 minutes by default.

**How do I fix too_many_pings in grpc-go?** Either raise the client's `keepalive.ClientParameters.Time` to at least the server's MinTime, or lower the server's MinTime with `grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: ...})`. If the client sets `PermitWithoutStream: true`, the server also needs `PermitWithoutStream: true`.

**I set PermitWithoutStream: true on the server. Why do I still get too_many_pings?** MinTime is still 5 minutes when you don't set it. Both fields are needed.

**My client's Time is 5 seconds. Why does it ping every 10?** grpc-go raises a client Time below 10s to 10s and logs "Adjusting keepalive ping interval to minimum period of 10s".

**Does it need to run my code or reach a server?** No. It parses Go source with the standard library and never builds, runs or dials anything.

**Is it an MCP server?** No. It's a skill with a Go program and a script that builds it, so Go is all it needs.

## Tested on

- Unit tests for every decision (defaults, the 10s minimum, the equal-to-MinTime boundary, MinTime 0, PermitWithoutStream on each side, server pings, positional literals, `math.MaxInt64`), constants through variables, packages and other packages of the module, values from flags, fields, parameters and fields changed after the literal, options passed in from a caller, test files, JSON output and every refused input. Integration tests build the binary and run it on both examples, on sonic-gnmi before and after its fix and with the old client against the new server and the reverse, on each example file cut after every line and every 37th byte with the other file intact (no cut may print a clean result or exit 0), and on empty, random, build output, test output and run log files in place of the client and then the server. Each broken input must exit 3 with no result, with and without `--fail-on never`. Tests run on Go 1.22.12 and Go 1.27.4, with `-race`.
- The examples' programs compile against grpc-go v1.85.0, and `examples/repro` was run with the settings in the table above.
- Real code, on 2026-11-24: sonic-gnmi before and after PR 634 (the finding matches its issue 619). Exit 0 with no finding: containerd, jaeger and buildkit, whose clients set no keepalive, and grpc-go itself, where KA103 notes come from its examples and benchmarks. dapr gave KA103 notes and one KA902: its placement server pings clients at an interval set by a flag, and the default of 2s keeps its 10s clients from pinging, so KA902 is the right answer there: the tool can't see that value, and with it the client doesn't ping at all. etcd, temporal, lnd and vitess take their keepalive values from configuration, so they get KA901 and KA902 warnings and no clean result.
- Not tested: services in other languages, clients behind a proxy or service mesh, and a fresh Claude session using the skill.

## Contributing

Contributions are welcome, from people and from people working with AI agents. See [CONTRIBUTING.md](CONTRIBUTING.md). Good first issues are [listed here](https://github.com/GillMacedo/toomanypings/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

### How pull requests work

1. **Open a PR.** Fork the repo, work on a branch, and keep it to one change. Write a title that says what changes for users, because it becomes the changelog line: `feat:` goes under Added and `fix:` under Fixed.
2. **Automatic check.** Code changes need tests, and `make test` must pass. Changes over 30 lines need a linked open issue (`Closes #<number>`), and a PR can have at most 800 changed lines. If something is missing, a comment tells you what to fix.
3. **Review.** Claude reviews each PR automatically. Code PRs also get a security review and are tested in a sandbox. The review then merges the PR or asks for changes, and a small problem may become a follow-up issue instead. A PR with no new commit 14 days after a change request is closed, and you can reopen it. PRs that change CI, agent settings, npm or Python dependencies, vendored code or build files can't be merged this way, and a comment says so. The same goes for anything the security review doesn't clear.
4. **Release.** A merge that changes what users install becomes a new version within about two hours, with a GitHub Release and, if the project has one, its npm package. Docs and CI changes wait for the next release.

## AI disclosure

This project was largely generated with Claude Code and reviewed by its maintainer.

## About me

I'm Giulliano Macedo, and I build developer tools at GoTech Solutions. Follow along or get in touch:

- GitHub: [GillMacedo](https://github.com/GillMacedo)
- LinkedIn: [giulliano](https://www.linkedin.com/in/giulliano/)
- X: [@giumacedo](https://x.com/giumacedo)

If `toomanypings` saved you some prompts, a star helps others find it.

---

Built by [Giulliano Macedo](https://github.com/GillMacedo), GoTech Solutions · [LinkedIn](https://www.linkedin.com/in/giulliano/) · [X](https://x.com/giumacedo)

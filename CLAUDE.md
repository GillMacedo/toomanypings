# toomanypings

Claude Code skill and Go CLI that reads grpc-go source and reports client keepalive settings (`keepalive.ClientParameters`) that the server's enforcement policy (`keepalive.EnforcementPolicy`, or its default) answers with GOAWAY ENHANCE_YOUR_CALM "too_many_pings".

## Architecture
All code is in `plugin/scripts`, one `package main` with no dependencies, so `go run` and `run.sh` need no setup. `scan.go` finds and parses files and refuses broken input, `eval.go` groups packages and evaluates constant durations and bools, `model.go` finds client settings, server policies, `grpc.NewServer` calls and server ping intervals, `check.go` compares them and builds findings, and `main.go` has flags, output and exit codes. See `docs/architecture.md`.

`examples/` is a separate Go module that needs grpc-go. `examples/repro` runs real clients and servers; it isn't part of `make test`.

## Commands
- `make test`: gofmt, vet, unit tests and integration tests in `tests/`, with `-race`
- `make lint`: plugin and marketplace validation
- `make demo`: run the examples
- `make repro`: run the live grpc-go reproduction (network, Go 1.26+)

## Conventions
- Standard library only. Never build, run or dial the code being checked.
- Input that can't be trusted (empty, binary, cut off, failed command output, nothing to compare) exits 3 with no result. Never print a clean result unless at least one client was found.
- A value that can't be read is a warning (KA901, KA902), never treated as a default.
- Each finding has a stable `KAnnn` code. Don't reuse codes.
- A claim about what gRPC does must come from its source or docs, linked in the pull request, and ideally from a run of `examples/repro`.
- Tests must not assume the machine they run on.

## Do and don't
- Do add a test for what a change flags and for what it must not flag, and keep `TestEveryLineCut` and `TestBrokenFilesEachSide` passing.
- Don't guess which server a client dials. Report what the source shows.

## Definition of done
`make test`, `make lint` and `make demo` pass, and `README.md`, `ROADMAP.md` and `CHANGELOG.md` are updated when relevant.

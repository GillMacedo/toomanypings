# Architecture

    Go files and folders
        --> scan.go:  find .go files (skip vendor, testdata, hidden, _test.go),
                      refuse broken input, parse with go/parser
        --> eval.go:  group files into packages (folder + package name, import
                      path from go.mod), evaluate durations and bools
        --> model.go: client settings, server policies, grpc.NewServer calls
                      on defaults, server ping intervals per package
        --> check.go: every client against every server policy, or against
                      the assumed policy when no server is in the input
        --> main.go:  text or JSON, exit code

- **Client settings** are `grpc.WithKeepaliveParams(arg)` calls, with the argument followed to a `keepalive.ClientParameters` literal, and literals that never reach such a call in their file. An argument that can't be followed is an unreadable setting (KA901).
- **Server policies** are `grpc.KeepaliveEnforcementPolicy(arg)` calls and `keepalive.EnforcementPolicy` literals. A `grpc.NewServer` call is a server on the default policy when its package sets no policy and either its options are built in its own function or nothing in the input sets a policy (ADR 0003).
- **Server pings.** `keepalive.ServerParameters` Time values in a server's package are attached to it. The longest one is used, because it is the one least likely to keep the client from pinging.
- **Values** are evaluated from literals, `time` units, conversions, arithmetic, local definitions assigned once, package-level constants, package variables never assigned again, and constants in other packages of the same module. Flags, fields and parameters are unknown (ADR 0002).
- **Decision** for a client with effective Time `t` (at least 10s) and a server with MinTime `m` (5m when 0): a quiet RPC gets GOAWAY when `t < m`. An idle connection gets GOAWAY when the client permits pings without streams and either the server doesn't (`t` under 2h) or `t < m`. Neither happens when the server's own ping interval is shorter than `t`.
- **Level.** A conflict with every server in the input is an error. With the assumed default policy, or with unreadable servers in the input, it is a warning. With some servers only, a note.

Key choices are in `decisions/`.

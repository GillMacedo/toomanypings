# 0003: A policy applies to its package, and to servers whose options come from a caller

**Decision:** a `grpc.NewServer` call counts as a server on the default policy only when its package sets no policy and its options are built in the same function, or when nothing in the input sets a policy.

**Why:** in sonic-gnmi the policy is built in `telemetry` and passed through to `gnmi_server`, which calls `grpc.NewServer(opts...)`. Counting that call as a second, default server would turn its fixed code into a false note. Tracing options across packages needs type information the tool doesn't load.

**Cost:** a package that runs two servers with one policy, or a server whose caller doesn't pass the policy, is treated as having it. The README lists this under limits.

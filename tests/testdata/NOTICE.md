# Test data sources

`sonic-gnmi-before` and `sonic-gnmi-after` hold four files each from [sonic-net/sonic-gnmi](https://github.com/sonic-net/sonic-gnmi), unchanged, with their paths kept:

- `pkg/interceptors/dpuproxy/proxy.go` (the client)
- `telemetry/telemetry.go`, `gnmi_server/server.go` and `dialout/dialout_server/dialout_server.go` (the servers)

`sonic-gnmi-before` is from commit 181841bf3fc1968e1f187ea4c0afda7a147bf96a, the parent of the fix. `sonic-gnmi-after` is from commit 1bd6083f3776ee7a08b35497503a4692772087fa, "Fix too_many_pings during DPU SetPackage and add ARM64 CI build (#634)", which fixed issue 619.

License: Apache License 2.0, Copyright 2016 Microsoft, Inc. See `LICENSE-sonic-gnmi`.

Running toomanypings on the whole repository at each commit gives the same findings as these four files.

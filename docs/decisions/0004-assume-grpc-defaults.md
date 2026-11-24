# 0004: With no server in the input, assume gRPC's default policy

**Decision:** compare clients with MinTime 5m and no pings without RPCs when no server is in the input, report conflicts as warnings, and let `--server-min-time` and `--server-permit-without-stream` describe the real server.

**Why:** clients often call servers in other repositories or languages. grpc-go, grpc-java (`DEFAULT_SERVER_PERMIT_KEEPALIVE_TIME_NANOS`) and gRPC C-core (`GRPC_ARG_HTTP2_MIN_RECV_PING_INTERVAL_WITHOUT_DATA_MS`) all default to 5 minutes and no pings without calls, so the default is the most likely policy. It is still an assumption, so the level is a warning and the message says so.

**Cost:** clients of servers that relaxed their policy get a warning until the flags are passed.

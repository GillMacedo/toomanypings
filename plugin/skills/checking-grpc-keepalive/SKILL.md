---
name: checking-grpc-keepalive
description: Checks grpc-go source for client keepalive settings that the server's enforcement policy rejects, which ends in GOAWAY ENHANCE_YOUR_CALM "too_many_pings" and RPCs failing with code Unavailable. Compares keepalive.ClientParameters (Time, PermitWithoutStream) with keepalive.EnforcementPolicy (MinTime, PermitWithoutStream) and the defaults of servers that set none. Use when the user sees too_many_pings or ENHANCE_YOUR_CALM, long calls or quiet streams that fail after a minute, idle gRPC connections that keep reconnecting, or wants keepalive settings reviewed.
---

# Checking gRPC keepalive

A grpc-go client with keepalive pings the server when an RPC is quiet for `Time`. A server accepts one ping per `MinTime` (5 minutes when it sets no `keepalive.EnforcementPolicy`, or sets MinTime to 0), and on a connection with no RPC it accepts none unless its `PermitWithoutStream` is true. After three early pings it sends GOAWAY `too_many_pings`.

## Run the tool

It needs Go 1.22 or newer. It only reads `.go` files.

    sh "${CLAUDE_PLUGIN_ROOT}/scripts/run.sh" PATH...

- Pass the folders of the client and of the server it calls. With only the client, the server is assumed to use gRPC's default policy. If the user knows the server's settings, add `--server-min-time 20s` and `--server-permit-without-stream`.
- Add `--format json` to read the findings in detail.

Exit codes: 0 means no problem found, 1 findings, 2 bad options, 3 the input can't be trusted or has nothing to compare. With exit 3 there is no result, so never say the settings are fine.

## Read the result

- `KA101`: the client pings during a quiet RPC more often than the server allows. RPCs fail with Unavailable about 4 × Time into the silence. This is the one that matches "my long call fails with too_many_pings".
- `KA102`: the client pings idle connections and the server doesn't permit it, so the connection closes and reopens.
- `KA103` (note): too frequent for some servers in the input. Find out which server this client dials before calling it a bug.
- `KA301` (note): Time below 10s is raised to 10s.
- `KA901`, `KA902`: a value comes from config or flags. Read where it is set, such as a config file or flag default, and compare by hand with the rule above. Don't call it fine without doing that.

## Report

1. For each finding, give the client's `file:line`, its Time and PermitWithoutStream, and the server policy it was compared with.
2. Propose the smallest fix, from the finding's Fix line: usually lower the server's MinTime to the client's Time (and set PermitWithoutStream true on both if idle pings are wanted), or raise the client's Time. Lowering MinTime only matters if the server is one you control.
3. Say what wasn't checked: other languages, proxies or meshes between client and server (pings stop at the proxy), and values from configuration.

## Don't

Don't edit files unless the user asks. Don't call the settings safe when the tool exited 3 or reported KA901 or KA902.

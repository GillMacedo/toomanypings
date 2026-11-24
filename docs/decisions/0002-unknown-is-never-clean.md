# 0002: Refuse untrusted input, and never treat an unreadable value as a default

**Decision:** exit 3 with no result for empty, binary, unparsable or failed-command input, and when no file imports grpc or no client exists. A keepalive value from configuration, a flag or a parameter is a warning (KA901, KA902), which fails under the default `--fail-on warn`, and the result line says the result isn't clean.

**Why:** a checker that reports "no mismatch" when it couldn't see the values is worse than no checker. Earlier tools in this series reported an unreadable report or a failed build as all clear. Large projects such as etcd and temporal read keepalive from configuration, and guessing the default there would print a clean result for code that may have the bug.

**Cost:** those projects exit 1 until someone checks the configured values. `--fail-on error` lets them pass in CI while the warnings stay visible.

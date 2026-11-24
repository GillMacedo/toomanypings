# toomanypings plugin

This plugin adds the `checking-grpc-keepalive` skill. It compares grpc-go client keepalive settings with the servers' enforcement policies in Go source, and finds clients that the server will disconnect with GOAWAY ENHANCE_YOUR_CALM "too_many_pings". It reads source files only and never builds, runs or dials anything.

It needs Go 1.22 or newer: `scripts/run.sh` builds the tool from the bundled source each time it runs. See the repository README for usage, findings and limits.

Install it from this repository's marketplace:

    /plugin install toomanypings --marketplace GillMacedo/toomanypings

# Changelog

All notable changes are documented here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. This project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-11-24

### Added
- Read grpc-go source and compare every `keepalive.ClientParameters` with every `keepalive.EnforcementPolicy` and with `grpc.NewServer` calls that set none.
- KA101 for clients that ping during quiet RPCs more often than the server's MinTime, and KA102 for idle-connection pings the server doesn't permit, with the time to the GOAWAY and a fix.
- grpc-go's rules: the 10s client minimum, MinTime 0 meaning 5 minutes, the 2 hour limit without RPCs, server pings that keep the client from pinging, and the one-time doubling of Time after a GOAWAY.
- Values from literals, constants, variables and other packages of the module; values from config, flags or parameters are reported as unreadable (KA901, KA902), never as clean.
- `--server-min-time` and `--server-permit-without-stream` for a server that isn't in the input, `--format json`, `--fail-on` and `--tests`.
- Exit code 3, with no result, for empty, garbage, cut-off or failed-command input, and when there is nothing to compare.
- `checking-grpc-keepalive` skill and plugin manifest.

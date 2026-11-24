# Security

Report vulnerabilities through [GitHub's private vulnerability reporting](https://github.com/GillMacedo/toomanypings/security/advisories/new), not in a public issue.

A case where `toomanypings` misses a keepalive mismatch or reports one that gRPC doesn't have is a normal bug. If a wrong answer could lead someone to weaken a server's ping protection in a way that matters, such as setting a very low MinTime on a public server, report it privately. `toomanypings` is a diagnostic aid. It reads files and doesn't change or enforce anything.

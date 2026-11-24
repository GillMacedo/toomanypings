# 0001: A skill with Go source, not an MCP server

**Decision:** ship a Claude Code skill whose script builds a standard-library Go program from source on each run.

**Why:** the check is a pure function of source files. An agent gets everything it needs by running one command and reading the text or JSON output, so an MCP server would add a process and a package to install without adding a capability. The users are Go developers, so Go is already installed, and the standard library has a complete Go parser.

**Cost:** each run compiles the tool, which takes a few seconds the first time.

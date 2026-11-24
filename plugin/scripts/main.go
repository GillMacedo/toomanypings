// Command toomanypings reads grpc-go source and finds client keepalive
// settings that the server's enforcement policy punishes with GOAWAY
// ENHANCE_YOUR_CALM "too_many_pings".
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const version = "0.1.0"

const footer = "toomanypings reads grpc-go source only. Values from config files and flags, other gRPC languages, and proxies between client and server aren't seen."

const usageText = `Usage: toomanypings [--format text|json] [--fail-on warn|error|never] [--tests]
                    [--server-min-time DURATION] [--server-permit-without-stream]
                    PATH...

PATH is a Go file or a folder, read recursively. Pass the client's and the
server's code. vendor, testdata, hidden folders and _test.go files are skipped.

Exit codes: 0 no finding at the --fail-on level, 1 findings, 2 bad options,
3 the input can't be trusted or has nothing to compare (no result).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type report struct {
	Version  string    `json:"version"`
	Clients  int       `json:"clientSettings"`
	Servers  int       `json:"serverPolicies"`
	Assumed  bool      `json:"serverAssumed"`
	Against  string    `json:"comparedWith"`
	Findings []Finding `json:"findings"`
	Result   string    `json:"result"`
}

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("toomanypings", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	format := fl.String("format", "text", "")
	failOn := fl.String("fail-on", "warn", "")
	tests := fl.Bool("tests", false, "")
	serverMin := fl.Duration("server-min-time", 0, "")
	serverPWS := fl.Bool("server-permit-without-stream", false, "")
	showVersion := fl.Bool("version", false, "")
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usageText)
			return 0
		}
		fmt.Fprintf(stderr, "toomanypings: %v\n\n%s", err, usageText)
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "toomanypings", version)
		return 0
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "toomanypings: --format must be text or json\n")
		return 2
	}
	var failLevel level
	switch *failOn {
	case "warn":
		failLevel = levelWarn
	case "error":
		failLevel = levelError
	case "never":
		failLevel = levelError + 1
	default:
		fmt.Fprintf(stderr, "toomanypings: --fail-on must be warn, error or never\n")
		return 2
	}
	if *serverMin < 0 {
		fmt.Fprintf(stderr, "toomanypings: --server-min-time can't be negative\n")
		return 2
	}
	if fl.NArg() == 0 {
		fmt.Fprintf(stderr, "toomanypings: no PATH given\n\n%s", usageText)
		return 2
	}
	assume := assumption{minTime: defaultMinTime}
	fl.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "server-min-time":
			assume.fromFlag = true
			assume.minTime = float64(*serverMin)
			if *serverMin == 0 {
				assume.minTime = defaultMinTime
			}
		case "server-permit-without-stream":
			assume.fromFlag = true
			assume.pws = *serverPWS
		}
	})

	rep, err := analyze(fl.Args(), *tests, assume)
	if err != nil {
		var ie *inputError
		if errors.As(err, &ie) {
			fmt.Fprintf(stderr, "toomanypings: can't trust this input, so there is no result.\n%s\n", ie.msg)
			return 3
		}
		fmt.Fprintf(stderr, "toomanypings: %v\n", err)
		return 3
	}
	failing := 0
	for _, f := range rep.Findings {
		if f.lvl >= failLevel {
			failing++
		}
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return 3
		}
	} else {
		writeText(stdout, rep)
	}
	if failing > 0 {
		return 1
	}
	return 0
}

func analyze(paths []string, tests bool, assume assumption) (*report, error) {
	files, err := collectFiles(paths, tests)
	if err != nil {
		return nil, err
	}
	fset, parsed, err := parseFiles(files)
	if err != nil {
		return nil, err
	}
	prog := newProgram(fset, parsed)
	m := build(prog)
	if m.grpcFiles == 0 {
		return nil, refuse("none of the %d Go %s imports google.golang.org/grpc or google.golang.org/grpc/keepalive, so there is nothing to check. Pass the folders that create the gRPC clients and servers.", len(files), plural(len(files), "file", "files"))
	}
	if len(m.clients) == 0 && len(m.plainClients) == 0 {
		return nil, refuse("no gRPC client in these files (grpc.NewClient, grpc.Dial, grpc.DialContext or keepalive.ClientParameters), so there is nothing to compare with the %d server %s. Pass the client's code too.", len(m.servers), plural(len(m.servers), "policy", "policies"))
	}
	findings := check(m, assume)
	for i := range findings {
		findings[i].Level = findings[i].lvl.String()
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].lvl != findings[j].lvl {
			return findings[i].lvl > findings[j].lvl
		}
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	rep := &report{Version: version, Clients: len(m.clients), Servers: len(m.servers), Assumed: len(m.servers) == 0, Findings: findings}
	if rep.Findings == nil {
		rep.Findings = []Finding{}
	}
	rep.Against = fmt.Sprintf("%d server %s", rep.Servers, plural(rep.Servers, "policy", "policies"))
	if rep.Assumed && assume.fromFlag {
		rep.Against = fmt.Sprintf("the server policy from the flags (MinTime %s, PermitWithoutStream %t)", formatDuration(assume.minTime), assume.pws)
	} else if rep.Assumed {
		rep.Against = "gRPC's default server policy (no server in these files)"
	}
	rep.Result = resultLine(m, findings)
	return rep, nil
}

func resultLine(m *model, findings []Finding) string {
	problems, unread := 0, 0
	for _, f := range findings {
		if f.lvl == levelNote {
			continue
		}
		if strings.HasPrefix(f.Code, "KA9") {
			unread++
		} else {
			problems++
		}
	}
	switch {
	case problems > 0 && unread > 0:
		return fmt.Sprintf("%d %s where keepalive pings get connections closed, and %d %s that couldn't be read.", problems, plural(problems, "finding", "findings"), unread, plural(unread, "setting", "settings"))
	case problems > 0:
		return fmt.Sprintf("%d %s where keepalive pings get connections closed.", problems, plural(problems, "finding", "findings"))
	case unread > 0:
		return fmt.Sprintf("%d keepalive %s couldn't be read, so this isn't a clean result.", unread, plural(unread, "setting", "settings"))
	case len(m.clients) == 0:
		return fmt.Sprintf("none of the %d gRPC %s sets keepalive, so none sends keepalive pings.", len(m.plainClients), plural(len(m.plainClients), "client", "clients"))
	}
	return "every client keepalive setting is accepted by the server policies it was compared with."
}

func writeText(w io.Writer, rep *report) {
	fmt.Fprintf(w, "toomanypings: %d client keepalive %s checked against %s\n\n", rep.Clients, plural(rep.Clients, "setting", "settings"), rep.Against)
	for _, f := range rep.Findings {
		fmt.Fprintf(w, "%s %s %s:%d  %s\n", strings.ToUpper(f.Level), f.Code, f.File, f.Line, f.Message)
		for _, d := range f.Details {
			fmt.Fprintf(w, "  %s\n", d)
		}
		if f.Fix != "" {
			fmt.Fprintf(w, "  Fix: %s\n", f.Fix)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Result: %s\n%s\n", rep.Result, footer)
}

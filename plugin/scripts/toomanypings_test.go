package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write creates files under a new temp folder and returns its path.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runArgs(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return out.String(), errb.String(), code
}

func runJSON(t *testing.T, args ...string) (report, int) {
	t.Helper()
	out, errText, code := runArgs(t, append([]string{"--format", "json"}, args...)...)
	var rep report
	if code == 0 || code == 1 {
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("bad JSON (%v): %s %s", err, out, errText)
		}
	}
	return rep, code
}

func codes(rep report) []string {
	var out []string
	for _, f := range rep.Findings {
		out = append(out, f.Code+":"+f.Level)
	}
	return out
}

func client(params string) string {
	return `package client

import (
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

var _ = time.Second

func Dial() (*grpc.ClientConn, error) {
	return grpc.NewClient("svc:443", grpc.WithKeepaliveParams(keepalive.ClientParameters{` + params + `}))
}
`
}

func server(opts string) string {
	return `package server

import (
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

var _ = time.Second
var _ keepalive.EnforcementPolicy

func New() *grpc.Server {
	return grpc.NewServer(` + opts + `)
}
`
}

const policy = `grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{%s})`

func pol(fields string) string { return strings.Replace(policy, "%s", fields, 1) }

func TestDecisions(t *testing.T) {
	cases := []struct {
		name   string
		client string
		server string
		want   []string
		code   int
	}{
		{"default server, 10s client", "Time: 10 * time.Second", "", []string{"KA101:error"}, 1},
		{"default server, pws client", "Time: 10 * time.Second, PermitWithoutStream: true", "", []string{"KA101:error", "KA102:warn"}, 1},
		{"policy allows", "Time: 30 * time.Second, PermitWithoutStream: true", pol("MinTime: 20 * time.Second, PermitWithoutStream: true"), nil, 0},
		{"equal is allowed", "Time: 10 * time.Second", pol("MinTime: 10 * time.Second"), nil, 0},
		{"one nanosecond short", "Time: 20*time.Second - 1", pol("MinTime: 20 * time.Second"), []string{"KA101:error"}, 1},
		{"just under 10s is raised to 10s", "Time: 10*time.Second - 1", pol("MinTime: 10 * time.Second"), []string{"KA301:note"}, 0},
		{"server pws true but MinTime unset is still 5m", "Time: 10 * time.Second, PermitWithoutStream: true", pol("PermitWithoutStream: true"), []string{"KA101:error", "KA102:warn"}, 1},
		{"idle only: server MinTime fine, pws false", "Time: 30 * time.Second, PermitWithoutStream: true", pol("MinTime: 20 * time.Second"), []string{"KA102:warn"}, 1},
		{"client at 5m", "Time: 5 * time.Minute", "", nil, 0},
		{"client Time unset pings every 10s", "PermitWithoutStream: true", pol("MinTime: 5 * time.Second"), []string{"KA102:warn", "KA301:note"}, 1},
		{"client 3s raised to 10s, server 10s", "Time: 3 * time.Second", pol("MinTime: 10 * time.Second"), []string{"KA301:note"}, 0},
		{"server pings first", "Time: 10 * time.Second, PermitWithoutStream: true", "grpc.KeepaliveParams(keepalive.ServerParameters{Time: 2 * time.Second})", nil, 0},
		{"server pings, but slower than the client", "Time: 10 * time.Second", "grpc.KeepaliveParams(keepalive.ServerParameters{Time: 15 * time.Second})", []string{"KA101:error"}, 1},
		{"positional literal", "10 * time.Second, 5 * time.Second, true", "", []string{"KA101:error", "KA102:warn"}, 1},
		{"MaxInt64 never pings", "Time: math.MaxInt64", "", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := client(tc.client)
			if strings.Contains(tc.client, "math.") {
				c = strings.Replace(c, `"time"`, `"math"
	"time"`, 1)
			}
			dir := write(t, map[string]string{"client/c.go": c, "server/s.go": server(tc.server)})
			rep, code := runJSON(t, dir)
			got := codes(rep)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") || code != tc.code {
				t.Fatalf("got %v exit %d, want %v exit %d\n%+v", got, code, tc.want, tc.code, rep.Findings)
			}
		})
	}
}

func TestConstants(t *testing.T) {
	cases := map[string]string{
		"package const":       "Time: every",
		"local const":         "Time: local",
		"conversion":          "Time: time.Duration(10) * time.Second",
		"parens and floats":   "Time: (time.Second * 15) / 1.5",
		"other package":       "Time: config.PingEvery",
		"millisecond literal": "Time: 10000 * time.Millisecond",
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			c := `package client

import (
	"time"

	"example.com/app/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

const every = 10 * time.Second

var _ = config.PingEvery

func Dial() {
	const local = 10 * time.Second
	kp := keepalive.ClientParameters{` + field + `}
	grpc.NewClient("x", grpc.WithKeepaliveParams(kp))
}
`
			dir := write(t, map[string]string{
				"go.mod":           "module example.com/app\n\ngo 1.22\n",
				"client/c.go":      c,
				"config/config.go": "package config\n\nimport \"time\"\n\nconst PingEvery = 10 * time.Second\n",
				"server/s.go":      server(""),
			})
			rep, code := runJSON(t, dir)
			if code != 1 || len(rep.Findings) != 1 || rep.Findings[0].Code != "KA101" || rep.Clients != 1 {
				t.Fatalf("exit %d %+v", code, rep)
			}
			if !strings.Contains(rep.Findings[0].Message, "every 10s") {
				t.Fatalf("value not read: %s", rep.Findings[0].Message)
			}
		})
	}
}

func TestUnknownValues(t *testing.T) {
	cases := map[string]string{
		"flag":       `Time: *flag.Duration("ka", 10*time.Second, "")`,
		"field":      "Time: cfg.Time",
		"param":      "Time: d",
		"reassigned": "Time: v",
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			c := `package client

import (
	"flag"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

var _ = flag.Parse

type config struct{ Time time.Duration }

func Dial(cfg config, d time.Duration) {
	v := time.Second
	if d > 0 {
		v = d
	}
	grpc.NewClient("x", grpc.WithKeepaliveParams(keepalive.ClientParameters{` + field + `}))
}
`
			dir := write(t, map[string]string{"client/c.go": c, "server/s.go": server("")})
			rep, code := runJSON(t, dir)
			if code != 1 || len(rep.Findings) != 1 || rep.Findings[0].Code != "KA901" {
				t.Fatalf("exit %d %+v", code, rep.Findings)
			}
			if strings.Contains(rep.Result, "accepted") {
				t.Fatalf("unknown value printed as clean: %s", rep.Result)
			}
			// --fail-on error lets unknown values pass, but the result still
			// says they weren't checked.
			out, _, code := runArgs(t, "--fail-on", "error", dir)
			if code != 0 || !strings.Contains(out, "isn't a clean result") {
				t.Fatalf("exit %d\n%s", code, out)
			}
		})
	}
}

func TestUnknownServer(t *testing.T) {
	dir := write(t, map[string]string{
		"client/c.go": client("Time: 10 * time.Second"),
		"server/s.go": strings.Replace(server(pol("MinTime: minTime")), "func New()", "var minTime = time.Second\n\nfunc init() { minTime = 2 * time.Second }\n\nfunc New()", 1),
	})
	rep, code := runJSON(t, dir)
	if code != 1 || strings.Join(codes(rep), ",") != "KA902:warn" {
		t.Fatalf("exit %d %v", code, codes(rep))
	}
	// An unknown server ping interval also makes the server unknown,
	// because it can decide whether the client pings at all.
	dir = write(t, map[string]string{
		"client/c.go": client("Time: 10 * time.Second"),
		"server/s.go": strings.Replace(server("grpc.KeepaliveParams(keepalive.ServerParameters{Time: d})"), "func New()", "func New(d time.Duration)", 1),
	})
	rep, code = runJSON(t, dir)
	if code != 1 || strings.Join(codes(rep), ",") != "KA902:warn" {
		t.Fatalf("exit %d %v", code, codes(rep))
	}
}

func TestPartialIsANote(t *testing.T) {
	dir := write(t, map[string]string{
		"client/c.go": client("Time: 30 * time.Second"),
		"a/s.go":      server(pol("MinTime: 10 * time.Second")),
		"b/s.go":      server(""),
	})
	rep, code := runJSON(t, dir)
	if code != 0 || strings.Join(codes(rep), ",") != "KA103:note" {
		t.Fatalf("exit %d %v", code, codes(rep))
	}
}

func TestOptionsFromCaller(t *testing.T) {
	// The policy is built in main and passed to a package that calls
	// grpc.NewServer, as in sonic-gnmi. That server isn't a second,
	// default-policy server.
	mainGo := `package main

import (
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

func main() {
	var opts []grpc.ServerOption
	opts = append(opts, grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 5 * time.Second}))
	start(opts)
}
`
	srv := `package main

import "google.golang.org/grpc"

func start(common []grpc.ServerOption) {
	all := append([]grpc.ServerOption{}, common...)
	grpc.NewServer(all...)
}
`
	dir := write(t, map[string]string{"cmd/main.go": mainGo, "srv/start.go": strings.Replace(srv, "package main", "package srv", 1), "client/c.go": client("Time: 10 * time.Second")})
	rep, code := runJSON(t, dir)
	if code != 0 || len(rep.Findings) != 0 || rep.Servers != 1 {
		t.Fatalf("exit %d servers %d %v", code, rep.Servers, codes(rep))
	}
	// With no policy anywhere, the same server runs on defaults.
	dir = write(t, map[string]string{"srv/start.go": strings.Replace(srv, "package main", "package srv", 1), "client/c.go": client("Time: 10 * time.Second")})
	rep, code = runJSON(t, dir)
	if code != 1 || strings.Join(codes(rep), ",") != "KA101:error" {
		t.Fatalf("exit %d %v", code, codes(rep))
	}
}

func TestLiteralThroughVariableCountedOnce(t *testing.T) {
	c := `package client

import (
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

var kacp = keepalive.ClientParameters{Time: 10 * time.Second}

func Dial() {
	kp := keepalive.ClientParameters{Time: time.Minute}
	grpc.NewClient("a", grpc.WithKeepaliveParams(kp))
	grpc.NewClient("b", grpc.WithKeepaliveParams(kacp))
}
`
	dir := write(t, map[string]string{"client/c.go": c, "server/s.go": server(pol("MinTime: 30 * time.Second"))})
	rep, code := runJSON(t, dir)
	if rep.Clients != 2 || code != 1 || strings.Join(codes(rep), ",") != "KA101:error" {
		t.Fatalf("clients %d exit %d %v", rep.Clients, code, codes(rep))
	}
}

func TestNoServerAssumesDefaults(t *testing.T) {
	dir := write(t, map[string]string{"client/c.go": client("Time: 10 * time.Second")})
	rep, code := runJSON(t, dir)
	if code != 1 || strings.Join(codes(rep), ",") != "KA101:warn" || !rep.Assumed {
		t.Fatalf("exit %d %v", code, codes(rep))
	}
	rep, code = runJSON(t, "--server-min-time", "5s", "--server-permit-without-stream", dir)
	if code != 0 || len(rep.Findings) != 0 {
		t.Fatalf("exit %d %v", code, codes(rep))
	}
	rep, code = runJSON(t, "--server-min-time", "1m", dir)
	if code != 1 || strings.Join(codes(rep), ",") != "KA101:error" {
		t.Fatalf("exit %d %v", code, codes(rep))
	}
}

func TestClientsWithoutKeepalive(t *testing.T) {
	c := `package client

import "google.golang.org/grpc"

func Dial() { grpc.NewClient("x") }
`
	dir := write(t, map[string]string{"client/c.go": c, "server/s.go": server("")})
	out, _, code := runArgs(t, dir)
	if code != 0 || !strings.Contains(out, "none of the 1 gRPC client sets keepalive") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestTestFilesSkipped(t *testing.T) {
	dir := write(t, map[string]string{
		"client/c.go":      client("Time: 10 * time.Second"),
		"server/s.go":      server(""),
		"server/s_test.go": strings.Replace(server(pol("MinTime: time.Second")), "func New()", "func newTest()", 1),
	})
	rep, code := runJSON(t, dir)
	if code != 1 || rep.Servers != 1 {
		t.Fatalf("exit %d servers %d", code, rep.Servers)
	}
	rep, _ = runJSON(t, "--tests", dir)
	if rep.Servers != 1 || len(rep.Findings) != 0 {
		// The test file's policy applies to the package, so the
		// NewServer call is no longer a default-policy server.
		t.Fatalf("with --tests: servers %d %v", rep.Servers, codes(rep))
	}
}

func TestNothingToCompare(t *testing.T) {
	cases := map[string]map[string]string{
		"no grpc":   {"a/a.go": "package a\n\nfunc A() {}\n"},
		"no client": {"server/s.go": server("")},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			out, errText, code := runArgs(t, write(t, files))
			if code != 3 || out != "" || !strings.Contains(errText, "no result") {
				t.Fatalf("exit %d\nstdout %q\nstderr %s", code, out, errText)
			}
		})
	}
}

// brokenInputs are files that must never produce a result.
func brokenInputs() map[string][]byte {
	real := client("Time: 10 * time.Second")
	return map[string][]byte{
		"empty":           {},
		"whitespace":      []byte(" \n\t\n"),
		"binary":          {0x7f, 'E', 'L', 'F', 0, 1, 2, 0xff, 0xfe},
		"random text":     []byte("lorem ipsum dolor sit amet\nconsectetur\n"),
		"cut in half":     []byte(real[:len(real)/2]),
		"cut mid-literal": []byte(real[:strings.Index(real, "Time:")+8]),
		"failed build":    []byte("# example.com/app/client\n./c.go:13:9: undefined: grpc.NewClent\n"),
		"failed run":      []byte("panic: runtime error: invalid memory address or nil pointer dereference\n[signal SIGSEGV]\n\ngoroutine 1 [running]:\n"),
		"go error":        []byte("go: cannot find main module, but found .git/config\n"),
		"not utf-8":       []byte("package client\n\nvar s = \"\xff\xfe\"\n"),
	}
}

func TestBrokenInputEachSide(t *testing.T) {
	for name, data := range brokenInputs() {
		for _, side := range []string{"client/c.go", "server/s.go"} {
			t.Run(name+" "+side, func(t *testing.T) {
				files := map[string]string{"client/c.go": client("Time: 10 * time.Second"), "server/s.go": server(pol("MinTime: 5 * time.Second"))}
				files[side] = string(data)
				dir := write(t, files)
				for _, extra := range [][]string{nil, {"--fail-on", "never"}, {"--format", "json"}} {
					out, errText, code := runArgs(t, append(extra, dir)...)
					if code != 3 || out != "" || !strings.Contains(errText, "no result") {
						t.Fatalf("%v: exit %d\nstdout %q\nstderr %s", extra, code, out, errText)
					}
				}
				// The broken file passed by itself, next to the real folder.
				p := filepath.Join(dir, filepath.FromSlash(side))
				other := filepath.Join(dir, "client")
				if strings.HasPrefix(side, "client") {
					other = filepath.Join(dir, "server")
				}
				if _, _, code := runArgs(t, p, other); code != 3 {
					t.Fatalf("file argument: exit %d", code)
				}
			})
		}
	}
}

func TestBadArguments(t *testing.T) {
	dir := write(t, map[string]string{"client/c.go": client("Time: 10 * time.Second")})
	log := filepath.Join(dir, "build.log")
	os.WriteFile(log, []byte("ok\n"), 0o644)
	empty := filepath.Join(dir, "empty")
	os.Mkdir(empty, 0o755)
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{}, 2},
		{[]string{"--format", "xml", dir}, 2},
		{[]string{"--fail-on", "sometimes", dir}, 2},
		{[]string{"--server-min-time", "-1s", dir}, 2},
		{[]string{"--nope", dir}, 2},
		{[]string{filepath.Join(dir, "missing")}, 3},
		{[]string{log}, 3},
		{[]string{empty}, 3},
		{[]string{"--version"}, 0},
	} {
		if _, _, code := runArgs(t, tc.args...); code != tc.code {
			t.Errorf("%v: exit %d, want %d", tc.args, code, tc.code)
		}
	}
}

func TestFormatting(t *testing.T) {
	for ns, want := range map[float64]string{10e9: "10s", 300e9: "5m", 7200e9: "2h", 90e9: "1m30s"} {
		if got := formatDuration(ns); got != want {
			t.Errorf("formatDuration(%v) = %s, want %s", ns, got, want)
		}
	}
	for ns, want := range map[float64]string{10e9: "10 * time.Second", 60e9: "time.Minute", 1.5e9: "1500 * time.Millisecond"} {
		if got := goDuration(ns); got != want {
			t.Errorf("goDuration(%v) = %s, want %s", ns, got, want)
		}
	}
}

func TestFieldChangedAfterLiteral(t *testing.T) {
	c := `package client

import (
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

func Dial() {
	kp := keepalive.ClientParameters{Time: 10 * time.Minute}
	kp.Time = 10 * time.Second
	grpc.NewClient("a", grpc.WithKeepaliveParams(kp))
}
`
	dir := write(t, map[string]string{"client/c.go": c, "server/s.go": server("")})
	rep, code := runJSON(t, dir)
	if code != 1 || strings.Join(codes(rep), ",") != "KA901:warn" || strings.Contains(rep.Result, "accepted") {
		t.Fatalf("exit %d %v %s", code, codes(rep), rep.Result)
	}
	// The same for a server policy kept in a package variable.
	s := strings.Replace(server("grpc.KeepaliveEnforcementPolicy(pol)"), "func New()", "var pol = keepalive.EnforcementPolicy{MinTime: time.Second}\n\nfunc init() { pol.MinTime = time.Hour }\n\nfunc New()", 1)
	dir = write(t, map[string]string{"client/c.go": client("Time: 10 * time.Second"), "server/s.go": s})
	rep, code = runJSON(t, dir)
	if code != 1 || strings.Join(codes(rep), ",") != "KA902:warn" {
		t.Fatalf("exit %d %v", code, codes(rep))
	}
}

// Package tests builds toomanypings and runs it the way users do: on the
// examples, on real code from sonic-gnmi before and after its
// too_many_pings fix, and on broken input.
package tests

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "toomanypings-test")
	if err != nil {
		panic(err)
	}
	name := "toomanypings"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin = filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, "./plugin/scripts")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = ".."
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

const clean = "Result: every client keepalive setting is accepted"

func TestExamples(t *testing.T) {
	out, _, code := run(t, "examples/mismatch")
	if code != 1 || !strings.Contains(out, "ERROR KA101 examples/mismatch/client/main.go:19") || !strings.Contains(out, "WARN KA102") {
		t.Fatalf("mismatch: exit %d\n%s", code, out)
	}
	out, _, code = run(t, "--fail-on", "never", "examples/mismatch")
	if code != 0 || strings.Contains(out, clean) {
		t.Fatalf("mismatch --fail-on never: exit %d\n%s", code, out)
	}
	out, _, code = run(t, "examples/healthy")
	if code != 0 || !strings.Contains(out, clean) {
		t.Fatalf("healthy: exit %d\n%s", code, out)
	}
	out, errText, code := run(t, "examples/broken", "examples/healthy/server")
	if code != 3 || out != "" || !strings.Contains(errText, "failed command") {
		t.Fatalf("broken: exit %d\n%s%s", code, out, errText)
	}
	// Only the client: compared with gRPC's default server policy.
	out, _, code = run(t, "examples/healthy/client")
	if code != 1 || !strings.Contains(out, "WARN KA101") || !strings.Contains(out, "assumes the server uses gRPC's default policy") {
		t.Fatalf("client only: exit %d\n%s", code, out)
	}
	out, _, code = run(t, "--server-min-time", "10s", "--server-permit-without-stream", "examples/healthy/client")
	if code != 0 || !strings.Contains(out, clean) {
		t.Fatalf("client only with flags: exit %d\n%s", code, out)
	}
	// Only the server: nothing to compare.
	if out, _, code := run(t, "examples/healthy/server"); code != 3 || out != "" {
		t.Fatalf("server only: exit %d\n%s", code, out)
	}
}

// TestSonicGnmi runs on sonic-gnmi files before and after the commit that
// fixed its too_many_pings bug (issue 619, PR 634).
func TestSonicGnmi(t *testing.T) {
	out, _, code := run(t, "tests/testdata/sonic-gnmi-before")
	if code != 1 || !strings.Contains(out, "ERROR KA101 tests/testdata/sonic-gnmi-before/pkg/interceptors/dpuproxy/proxy.go:184  Client pings every 10s") {
		t.Fatalf("before: exit %d\n%s", code, out)
	}
	out, _, code = run(t, "tests/testdata/sonic-gnmi-after")
	if code != 0 || !strings.Contains(out, clean) || !strings.Contains(out, "1 client keepalive setting checked against 1 server policy") {
		t.Fatalf("after: exit %d\n%s", code, out)
	}
	// The fixed client against the old server, and the old client against
	// the fixed server.
	out, _, code = run(t, "tests/testdata/sonic-gnmi-after/pkg", "tests/testdata/sonic-gnmi-before/gnmi_server", "tests/testdata/sonic-gnmi-before/telemetry", "tests/testdata/sonic-gnmi-before/dialout")
	if code != 1 || !strings.Contains(out, "Client pings every 30s") {
		t.Fatalf("new client, old server: exit %d\n%s", code, out)
	}
	out, _, code = run(t, "tests/testdata/sonic-gnmi-before/pkg", "tests/testdata/sonic-gnmi-after/gnmi_server", "tests/testdata/sonic-gnmi-after/telemetry", "tests/testdata/sonic-gnmi-after/dialout")
	if code != 1 || !strings.Contains(out, "Client pings every 10s during a quiet RPC, and the server allows one ping every 20s") {
		t.Fatalf("old client, new server: exit %d\n%s", code, out)
	}
}

// TestEveryLineCut cuts each example file after every line and at every
// 37th byte, with the other files intact. No cut may print a clean result
// or exit 0, whatever --fail-on says, except the full file of the healthy
// example.
func TestEveryLineCut(t *testing.T) {
	for _, ex := range []string{"mismatch", "healthy"} {
		for _, side := range []string{"client", "server"} {
			src, err := os.ReadFile(filepath.Join("..", "examples", ex, side, "main.go"))
			if err != nil {
				t.Fatal(err)
			}
			var cuts []int
			for i, b := range src {
				if b == '\n' && i+1 < len(src) {
					cuts = append(cuts, i+1)
				}
				if i%37 == 0 {
					cuts = append(cuts, i)
				}
			}
			for _, n := range cuts {
				dir := t.TempDir()
				other := "server"
				if side == "server" {
					other = "client"
				}
				copyFile(t, filepath.Join("..", "examples", ex, other, "main.go"), filepath.Join(dir, other, "main.go"))
				writeFile(t, filepath.Join(dir, side, "main.go"), src[:n])
				for _, extra := range [][]string{nil, {"--fail-on", "never"}} {
					out, _, code := run(t, append(extra, dir)...)
					if strings.Contains(out, clean) || strings.Contains(out, "none of the") {
						t.Fatalf("%s/%s cut at %d %v printed a clean result:\n%s", ex, side, n, extra, out)
					}
					if extra == nil && code == 0 {
						t.Fatalf("%s/%s cut at %d exited 0:\n%s", ex, side, n, out)
					}
				}
			}
		}
	}
}

func TestBrokenFilesEachSide(t *testing.T) {
	random := make([]byte, 512)
	rand.Read(random)
	buildOut, err := os.ReadFile(filepath.Join("..", "examples", "broken", "client.go"))
	if err != nil {
		t.Fatal(err)
	}
	inputs := map[string][]byte{
		"empty":        nil,
		"random bytes": random,
		"build output": buildOut,
		"test output":  []byte("--- FAIL: TestWatch (0.00s)\n    watch_test.go:12: rpc error: code = Unavailable\nFAIL\n"),
		"run output":   []byte("2026/11/24 13:11:18 ERROR: [transport] Client received GoAway with error code ENHANCE_YOUR_CALM\n"),
	}
	for name, data := range inputs {
		for _, side := range []string{"client", "server"} {
			dir := t.TempDir()
			other := "server"
			if side == "server" {
				other = "client"
			}
			copyFile(t, filepath.Join("..", "examples", "mismatch", other, "main.go"), filepath.Join(dir, other, "main.go"))
			writeFile(t, filepath.Join(dir, side, "main.go"), data)
			for _, extra := range [][]string{nil, {"--fail-on", "never"}, {"--format", "json"}} {
				out, errText, code := run(t, append(extra, dir)...)
				if code != 3 || out != "" || !strings.Contains(errText, "no result") {
					t.Fatalf("%s as %s %v: exit %d\n%s%s", name, side, extra, code, out, errText)
				}
			}
		}
	}
}

func TestRunScript(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	cmd := exec.Command("sh", filepath.Join("plugin", "scripts", "run.sh"), "examples/healthy")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), clean) {
		t.Fatalf("run.sh: %v\n%s", err, out)
	}
	cmd = exec.Command("sh", filepath.Join("plugin", "scripts", "run.sh"), "examples/broken")
	cmd.Dir = ".."
	err = cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("run.sh on broken input: %v", err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, to, data)
}

func writeFile(t *testing.T, p string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

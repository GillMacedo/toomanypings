package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// inputError means the input can't be trusted. main prints it and exits 3
// without a result.
type inputError struct{ msg string }

func (e *inputError) Error() string { return e.msg }

func refuse(format string, args ...any) error {
	return &inputError{msg: fmt.Sprintf(format, args...)}
}

// sourceFile is one parsed Go file.
type sourceFile struct {
	path string // as shown to the user
	dir  string // absolute directory, used to group packages
	ast  *ast.File
}

// collectFiles expands the arguments into Go files. Folders are walked
// recursively, skipping vendor, testdata and hidden folders. Test files are
// skipped unless includeTests is set, because test servers often use
// relaxed keepalive settings that production servers don't have.
func collectFiles(args []string, includeTests bool) ([]string, error) {
	var files []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, refuse("%s: %v", arg, errText(err))
		}
		if !info.IsDir() {
			if !strings.HasSuffix(arg, ".go") {
				return nil, refuse("%s: not a .go file or a folder. toomanypings reads Go source, not logs or build output", arg)
			}
			add(arg)
			continue
		}
		before := len(files)
		err = filepath.WalkDir(arg, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if p != arg && (name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return nil
			}
			if strings.HasSuffix(name, "_test.go") && !includeTests {
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			add(p)
			return nil
		})
		if err != nil {
			return nil, refuse("%s: %v", arg, errText(err))
		}
		if len(files) == before {
			return nil, refuse("%s: no .go files in this folder", arg)
		}
	}
	sort.Strings(files)
	return files, nil
}

func errText(err error) string {
	if pe, ok := err.(*fs.PathError); ok {
		return pe.Err.Error()
	}
	return err.Error()
}

// failedOutputPrefixes are how output from a failed go command, compiler or
// shell starts. A .go file that starts like this was overwritten by a
// redirect, so it is refused with a clearer message than the parse error.
var failedOutputPrefixes = []string{
	"# ", "go: ", "go build", "panic: ", "fatal error: ", "--- FAIL", "FAIL\t", "make: ", "make[", "error: ", "Error: ", "bash: ", "sh: ",
}

// parseFiles reads and parses every file, refusing anything that isn't
// complete, valid Go source.
func parseFiles(paths []string) (*token.FileSet, []*sourceFile, error) {
	fset := token.NewFileSet()
	var out []*sourceFile
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, refuse("%s: %v", p, errText(err))
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return nil, nil, refuse("%s: the file is empty", p)
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			return nil, nil, refuse("%s: binary data, not Go source", p)
		}
		trimmed := bytes.TrimLeft(data, " \t\r\n")
		for _, prefix := range failedOutputPrefixes {
			if bytes.HasPrefix(trimmed, []byte(prefix)) {
				line, _, _ := strings.Cut(string(trimmed), "\n")
				return nil, nil, refuse("%s: starts like the output of a failed command, not Go source: %q", p, truncate(line, 80))
			}
		}
		f, err := parser.ParseFile(fset, p, data, parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, refuse("%s", firstLine(err.Error()))
		}
		abs, err := filepath.Abs(filepath.Dir(p))
		if err != nil {
			return nil, nil, refuse("%s: %v", p, err)
		}
		out = append(out, &sourceFile{path: p, dir: abs, ast: f})
	}
	return fset, out, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

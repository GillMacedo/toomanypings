package main

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// pkg is the files of one Go package: one folder and one package name.
type pkg struct {
	dir        string
	name       string
	importPath string // empty when no go.mod was found
	files      []*sourceFile
	// decls holds package-level constants, and variables that are never
	// assigned again, by name.
	decls map[string]ast.Expr
	// declFile is the file each entry of decls is in, for its imports.
	declFile map[string]*sourceFile
}

// program is every package in the input, with lookups the evaluator needs.
type program struct {
	fset     *token.FileSet
	pkgs     []*pkg
	byImport map[string]*pkg
	fileOf   map[*ast.File]*sourceFile
	pkgOf    map[*sourceFile]*pkg
}

func newProgram(fset *token.FileSet, files []*sourceFile) *program {
	prog := &program{fset: fset, byImport: map[string]*pkg{}, fileOf: map[*ast.File]*sourceFile{}, pkgOf: map[*sourceFile]*pkg{}}
	index := map[string]*pkg{}
	modCache := map[string][2]string{}
	for _, f := range files {
		key := f.dir + "\x00" + f.ast.Name.Name
		p := index[key]
		if p == nil {
			p = &pkg{dir: f.dir, name: f.ast.Name.Name, decls: map[string]ast.Expr{}, declFile: map[string]*sourceFile{}}
			if root, mod := findModule(f.dir, modCache); mod != "" {
				rel, err := filepath.Rel(root, f.dir)
				if err == nil {
					if rel == "." {
						p.importPath = mod
					} else {
						p.importPath = mod + "/" + filepath.ToSlash(rel)
					}
				}
			}
			index[key] = p
			prog.pkgs = append(prog.pkgs, p)
			if p.importPath != "" && !strings.HasSuffix(p.name, "_test") {
				prog.byImport[p.importPath] = p
			}
		}
		p.files = append(p.files, f)
		prog.fileOf[f.ast] = f
		prog.pkgOf[f] = p
	}
	for _, p := range prog.pkgs {
		p.collectDecls()
	}
	return prog
}

// findModule returns the folder of the nearest go.mod above dir and its
// module path.
func findModule(dir string, cache map[string][2]string) (string, string) {
	if v, ok := cache[dir]; ok {
		return v[0], v[1]
	}
	var root, mod string
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module") {
				mod = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "module")), `"`)
				root = dir
				break
			}
		}
	} else if parent := filepath.Dir(dir); parent != dir {
		root, mod = findModule(parent, cache)
	}
	cache[dir] = [2]string{root, mod}
	return root, mod
}

func (p *pkg) collectDecls() {
	assigned := map[string]bool{}
	for _, f := range p.files {
		ast.Inspect(f.ast, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				if n.Tok != token.DEFINE {
					for _, l := range n.Lhs {
						if id, ok := l.(*ast.Ident); ok {
							assigned[id.Name] = true
						}
					}
				}
			case *ast.UnaryExpr:
				if id, ok := n.X.(*ast.Ident); ok && n.Op == token.AND {
					assigned[id.Name] = true
				}
			}
			return true
		})
	}
	for _, f := range p.files {
		for _, d := range f.ast.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, s := range gd.Specs {
				vs := s.(*ast.ValueSpec)
				if len(vs.Values) != len(vs.Names) {
					continue
				}
				for i, name := range vs.Names {
					if gd.Tok == token.VAR && assigned[name.Name] {
						continue
					}
					p.decls[name.Name] = vs.Values[i]
					p.declFile[name.Name] = f
				}
			}
		}
	}
}

// importName returns the name a file uses for an import path, or "".
func importName(f *ast.File, path string) string {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != path {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				return ""
			}
			return imp.Name.Name
		}
		return path[strings.LastIndex(path, "/")+1:]
	}
	return ""
}

func importPathOf(f *ast.File, name string) string {
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		local := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local == name {
			return path
		}
	}
	return ""
}

// scope is where an expression appears: its file and the enclosing
// function body, if any.
type scope struct {
	file *sourceFile
	fn   *ast.BlockStmt
	// params are the enclosing function's parameter names.
	params map[string]bool
}

// value is the result of evaluating an expression. known is false when the
// value comes from configuration, flags, function arguments or anything
// else that can't be read from the source.
type value struct {
	known bool
	num   float64 // nanoseconds for durations
	b     bool
	isB   bool
	why   string // when unknown: the expression and where it comes from
}

const maxDepth = 20

func (prog *program) eval(e ast.Expr, sc scope, depth int) value {
	if depth > maxDepth {
		return value{why: prog.text(e) + " (too many levels of constants)"}
	}
	switch e := e.(type) {
	case *ast.ParenExpr:
		return prog.eval(e.X, sc, depth+1)
	case *ast.BasicLit:
		if e.Kind == token.INT || e.Kind == token.FLOAT {
			v, err := strconv.ParseFloat(strings.ReplaceAll(e.Value, "_", ""), 64)
			if err != nil {
				if n, err2 := strconv.ParseInt(strings.ReplaceAll(e.Value, "_", ""), 0, 64); err2 == nil {
					return value{known: true, num: float64(n)}
				}
				return value{why: e.Value}
			}
			return value{known: true, num: v}
		}
	case *ast.UnaryExpr:
		v := prog.eval(e.X, sc, depth+1)
		if !v.known {
			return v
		}
		switch e.Op {
		case token.SUB:
			if !v.isB {
				v.num = -v.num
				return v
			}
		case token.ADD:
			return v
		case token.NOT:
			if v.isB {
				v.b = !v.b
				return v
			}
		}
	case *ast.BinaryExpr:
		x := prog.eval(e.X, sc, depth+1)
		if !x.known {
			return value{why: prog.text(e) + unknownTail(x)}
		}
		y := prog.eval(e.Y, sc, depth+1)
		if !y.known {
			return value{why: prog.text(e) + unknownTail(y)}
		}
		if x.isB || y.isB {
			break
		}
		switch e.Op {
		case token.MUL:
			return value{known: true, num: x.num * y.num}
		case token.ADD:
			return value{known: true, num: x.num + y.num}
		case token.SUB:
			return value{known: true, num: x.num - y.num}
		case token.QUO:
			if y.num != 0 {
				return value{known: true, num: x.num / y.num}
			}
		}
	case *ast.CallExpr:
		// Conversions such as time.Duration(30) and int64(5).
		if len(e.Args) == 1 && isConversion(e.Fun, sc.file.ast) {
			return prog.eval(e.Args[0], sc, depth+1)
		}
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && importPathOf(sc.file.ast, id.Name) == "flag" {
				return value{why: prog.text(e) + " (a command-line flag)"}
			}
		}
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			path := importPathOf(sc.file.ast, id.Name)
			if path == "math" && (e.Sel.Name == "MaxInt64" || e.Sel.Name == "MaxInt") {
				return value{known: true, num: math.MaxInt64}
			}
			if path == "time" {
				if d, ok := timeUnits[e.Sel.Name]; ok {
					return value{known: true, num: d}
				}
			}
			if path != "" {
				if other := prog.byImport[path]; other != nil {
					if expr, ok := other.decls[e.Sel.Name]; ok {
						return prog.eval(expr, scope{file: other.declFile[e.Sel.Name]}, depth+1)
					}
				}
				return value{why: prog.text(e) + " (from package " + path + ", which isn't in the input or isn't a constant)"}
			}
		}
		return value{why: prog.text(e) + " (a field, set at run time)"}
	case *ast.Ident:
		switch e.Name {
		case "true":
			return value{known: true, b: true, isB: true}
		case "false":
			return value{known: true, b: false, isB: true}
		}
		if sc.params[e.Name] {
			return value{why: e.Name + " (a function parameter)"}
		}
		if sc.fn != nil {
			if expr, ok, many := localDef(sc.fn, e.Name); ok {
				return prog.eval(expr, scope{file: sc.file, fn: sc.fn, params: sc.params}, depth+1)
			} else if many {
				return value{why: e.Name + " (assigned more than once)"}
			}
		}
		if p := prog.pkgOf[sc.file]; p != nil {
			if expr, ok := p.decls[e.Name]; ok {
				return prog.eval(expr, scope{file: p.declFile[e.Name]}, depth+1)
			}
		}
		return value{why: e.Name + " (not a constant this tool can read)"}
	}
	return value{why: prog.text(e)}
}

func unknownTail(v value) string {
	if i := strings.Index(v.why, " ("); i >= 0 {
		return ": " + strings.TrimSpace(v.why)
	}
	return ""
}

var timeUnits = map[string]float64{
	"Nanosecond":  1,
	"Microsecond": 1e3,
	"Millisecond": 1e6,
	"Second":      1e9,
	"Minute":      60e9,
	"Hour":        3600e9,
}

var conversionTypes = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
}

func isConversion(fun ast.Expr, f *ast.File) bool {
	switch fun := fun.(type) {
	case *ast.Ident:
		return conversionTypes[fun.Name]
	case *ast.SelectorExpr:
		id, ok := fun.X.(*ast.Ident)
		return ok && fun.Sel.Name == "Duration" && importPathOf(f, id.Name) == "time"
	case *ast.ParenExpr:
		return isConversion(fun.X, f)
	}
	return false
}

// localDef finds the single definition of name inside a function body. ok
// is false when there is none; many is true when the name is assigned
// more than once, so its value depends on the path taken.
func localDef(body *ast.BlockStmt, name string) (expr ast.Expr, ok, many bool) {
	count := 0
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, l := range n.Lhs {
				id, isID := l.(*ast.Ident)
				if !isID || id.Name != name {
					continue
				}
				count++
				if len(n.Rhs) == len(n.Lhs) && (n.Tok == token.DEFINE || n.Tok == token.ASSIGN) {
					expr = n.Rhs[i]
				} else {
					expr = nil
				}
			}
		case *ast.ValueSpec:
			for i, id := range n.Names {
				if id.Name != name {
					continue
				}
				count++
				if len(n.Values) == len(n.Names) {
					expr = n.Values[i]
				} else {
					expr = nil
				}
			}
		case *ast.UnaryExpr:
			if id, isID := n.X.(*ast.Ident); isID && n.Op == token.AND && id.Name == name {
				count += 2 // its address is taken, so anything can change it
			}
		}
		return true
	})
	if count > 1 {
		return nil, false, true
	}
	return expr, count == 1 && expr != nil, false
}

func (prog *program) text(e ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, prog.fset, e); err != nil {
		return "?"
	}
	s := strings.Join(strings.Fields(buf.String()), " ")
	return truncate(s, 100)
}

// formatDuration prints nanoseconds the way time.Duration does, without a
// trailing "0s" for whole minutes and hours.
func formatDuration(ns float64) string {
	if ns >= math.MaxInt64 {
		return "never"
	}
	s := time.Duration(int64(ns)).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

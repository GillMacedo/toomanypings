package main

import (
	"go/ast"
	"go/token"
	"math"
	"sort"
)

const (
	grpcPath      = "google.golang.org/grpc"
	keepalivePath = "google.golang.org/grpc/keepalive"
)

// grpc-go behavior these checks rely on, from google.golang.org/grpc
// v1.85.0 (keepalive/keepalive.go, dialoptions.go, server.go and
// internal/transport/http2_server.go).
var (
	minClientTime        = 10 * 1e9          // WithKeepaliveParams raises Time to 10s
	defaultMinTime       = 5 * 60 * 1e9      // EnforcementPolicy.MinTime when 0
	noStreamPingInterval = 2 * 60 * 60 * 1e9 // defaultPingTimeout, when PermitWithoutStream is false
)

// clientSetting is one keepalive.ClientParameters value.
type clientSetting struct {
	pos     token.Position
	via     string // how it was found, for the message
	time    value  // the Time field; unset means 0
	timeSet bool
	pws     value
	expr    string // when the whole value is unknown
}

// effectiveTime is the interval grpc-go uses: Time, raised to 10s.
func (c *clientSetting) effectiveTime() float64 {
	if !c.timeSet || c.time.num < minClientTime {
		return minClientTime
	}
	return c.time.num
}

func (c *clientSetting) known() bool {
	return c.expr == "" && (!c.timeSet || c.time.known) && c.pws.known
}

// serverPolicy is one server's enforcement policy: an explicit
// keepalive.EnforcementPolicy, or the default of a grpc.NewServer call
// that sets none.
type serverPolicy struct {
	pos      token.Position
	explicit bool
	minTime  value
	minSet   bool
	pws      value
	expr     string
	pkg      *pkg
	// pings are the ServerParameters.Time values set in the server's
	// package. A server that pings the client more often than the client's
	// Time keeps the client from pinging at all.
	pings []value
}

// pingTime is the longest interval at which the server pings the client:
// grpc-go's default of 2h, or the largest ServerParameters.Time in its
// package, raised to grpc-go's 1s minimum. ok is false when a value can't
// be read.
func (s *serverPolicy) pingTime() (float64, bool) {
	if len(s.pings) == 0 {
		return noStreamPingInterval, true
	}
	max := 0.0
	for _, v := range s.pings {
		if !v.known {
			return 0, false
		}
		t := v.num
		if t == 0 {
			t = noStreamPingInterval
		} else if t < 1e9 {
			t = 1e9
		}
		if t > max {
			max = t
		}
	}
	return max, true
}

func (s *serverPolicy) effectiveMinTime() float64 {
	if !s.minSet || s.minTime.num == 0 {
		return defaultMinTime
	}
	return s.minTime.num
}

func (s *serverPolicy) known() bool {
	_, ok := s.pingTime()
	return s.expr == "" && (!s.minSet || s.minTime.known) && s.pws.known && ok
}

// model is everything found in the input.
type model struct {
	clients      []*clientSetting
	servers      []*serverPolicy
	plainClients []token.Position // grpc.Dial, DialContext and NewClient calls
	newServers   int
	grpcFiles    int
}

func build(prog *program) *model {
	m := &model{}
	// Packages that call grpc.KeepaliveEnforcementPolicy anywhere, and
	// whether the input calls it at all.
	policyPkgs := map[*pkg]bool{}
	anyPolicy := false
	type pending struct {
		pos        token.Position
		fromCaller bool
		p          *pkg
	}
	var servers []pending
	pings := map[*pkg][]value{}

	for _, p := range prog.pkgs {
		for _, f := range p.files {
			grpcName := importName(f.ast, grpcPath)
			kaName := importName(f.ast, keepalivePath)
			if grpcName == "" && kaName == "" {
				continue
			}
			m.grpcFiles++
			seenLits := map[ast.Node]bool{}
			// Calls first, so a literal passed through a variable is
			// counted once, at the call.
			walkFuncs(f, func(n ast.Node, sc scope) {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return
				}
				switch selectorOf(call.Fun, grpcName) {
				case "WithKeepaliveParams":
					if len(call.Args) == 1 {
						c := prog.clientFrom(call.Args[0], sc, seenLits)
						c.pos = prog.fset.Position(call.Pos())
						c.via = "grpc.WithKeepaliveParams"
						m.clients = append(m.clients, c)
					}
				case "KeepaliveEnforcementPolicy":
					policyPkgs[p] = true
					anyPolicy = true
					if len(call.Args) == 1 {
						s := prog.serverFrom(call.Args[0], sc, seenLits)
						s.pos = prog.fset.Position(call.Pos())
						s.pkg = p
						m.servers = append(m.servers, s)
					}
				case "KeepaliveParams":
					if len(call.Args) == 1 {
						pings[p] = append(pings[p], prog.serverPingFrom(call.Args[0], sc, seenLits))
					}
				case "NewServer":
					m.newServers++
					servers = append(servers, pending{pos: prog.fset.Position(call.Pos()), fromCaller: optionsFromCaller(call, sc), p: p})
				case "Dial", "DialContext", "NewClient":
					m.plainClients = append(m.plainClients, prog.fset.Position(call.Pos()))
				}
			})
			// Then literals that never reach one of those calls in this
			// file, such as a default kept in a struct field.
			walkFuncs(f, func(n ast.Node, sc scope) {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || seenLits[lit] {
					return
				}
				switch selectorOf(lit.Type, kaName) {
				case "ClientParameters":
					c := prog.clientLit(lit, sc)
					c.pos = prog.fset.Position(lit.Pos())
					c.via = "keepalive.ClientParameters"
					m.clients = append(m.clients, c)
				case "EnforcementPolicy":
					anyPolicy = true
					policyPkgs[p] = true
					s := prog.serverLit(lit, sc)
					s.pos = prog.fset.Position(lit.Pos())
					s.pkg = p
					m.servers = append(m.servers, s)
				case "ServerParameters":
					pings[p] = append(pings[p], prog.serverPingLit(lit, sc))
				}
			})
		}
	}
	// A grpc.NewServer call runs with the default policy when its package
	// sets none, and its options are built there, or the input sets no
	// policy anywhere that a caller could pass in.
	for _, s := range servers {
		if policyPkgs[s.p] {
			continue
		}
		if s.fromCaller && anyPolicy {
			continue
		}
		m.servers = append(m.servers, &serverPolicy{pos: s.pos, pws: value{known: true, isB: true}, pkg: s.p})
	}
	for _, s := range m.servers {
		s.pings = pings[s.pkg]
	}
	sortModel(m)
	return m
}

func sortModel(m *model) {
	sort.SliceStable(m.clients, func(i, j int) bool { return less(m.clients[i].pos, m.clients[j].pos) })
	sort.SliceStable(m.servers, func(i, j int) bool { return less(m.servers[i].pos, m.servers[j].pos) })
}

func less(a, b token.Position) bool {
	if a.Filename != b.Filename {
		return a.Filename < b.Filename
	}
	return a.Offset < b.Offset
}

// walkFuncs visits every node with the scope of the function it is in.
func walkFuncs(f *sourceFile, visit func(ast.Node, scope)) {
	var walk func(n ast.Node, sc scope)
	walk = func(root ast.Node, sc scope) {
		ast.Inspect(root, func(n ast.Node) bool {
			if n == nil || n == root {
				return true
			}
			switch fn := n.(type) {
			case *ast.FuncDecl:
				if fn.Body != nil {
					walk(fn.Body, scope{file: sc.file, fn: fn.Body, params: paramNames(fn.Type, fn.Recv)})
				}
				return false
			case *ast.FuncLit:
				params := paramNames(fn.Type, nil)
				for k := range sc.params {
					params[k] = true
				}
				walk(fn.Body, scope{file: sc.file, fn: fn.Body, params: params})
				return false
			}
			visit(n, sc)
			return true
		})
	}
	walk(f.ast, scope{file: f})
}

func paramNames(t *ast.FuncType, recv *ast.FieldList) map[string]bool {
	out := map[string]bool{}
	for _, fl := range []*ast.FieldList{t.Params, recv} {
		if fl == nil {
			continue
		}
		for _, field := range fl.List {
			for _, n := range field.Names {
				out[n.Name] = true
			}
		}
	}
	return out
}

// selectorOf returns Sel when e is pkgName.Sel.
func selectorOf(e ast.Expr, pkgName string) string {
	if pkgName == "" {
		return ""
	}
	if st, ok := e.(*ast.StarExpr); ok {
		e = st.X
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != pkgName {
		return ""
	}
	return sel.Sel.Name
}

// optionsFromCaller reports whether a grpc.NewServer call gets its options
// from outside its function: a spread of a parameter, or of a local slice
// built from one.
func optionsFromCaller(call *ast.CallExpr, sc scope) bool {
	if !call.Ellipsis.IsValid() || len(call.Args) == 0 {
		return false
	}
	return dependsOnOutside(call.Args[len(call.Args)-1], sc, 0)
}

func dependsOnOutside(e ast.Expr, sc scope, depth int) bool {
	if depth > maxDepth {
		return true
	}
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.SelectorExpr:
			// A field such as s.opts or cfg.ServerOptions comes from
			// outside. pkg.Func calls are looked at through their args.
			if id, ok := n.X.(*ast.Ident); ok && sc.file != nil && importPathOf(sc.file.ast, id.Name) != "" {
				return false
			}
			found = true
			return false
		case *ast.Ident:
			if sc.params[n.Name] {
				found = true
				return false
			}
			if sc.fn == nil {
				return false
			}
			for _, rhs := range assignmentsTo(sc.fn, n.Name) {
				if dependsOnOutside(rhs, sc, depth+1) {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

// assignmentsTo returns every expression assigned to name in a function.
func assignmentsTo(body *ast.BlockStmt, name string) []ast.Expr {
	var out []ast.Expr
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, l := range n.Lhs {
				if id, ok := l.(*ast.Ident); ok && id.Name == name && i < len(n.Rhs) {
					out = append(out, n.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, id := range n.Names {
				if id.Name == name && i < len(n.Values) {
					out = append(out, n.Values[i])
				}
			}
		}
		return true
	})
	return out
}

// literalOf follows &x, and a variable defined once in the function or
// package, to a composite literal.
func (prog *program) literalOf(e ast.Expr, sc scope, depth int) (*ast.CompositeLit, scope) {
	if depth > maxDepth {
		return nil, sc
	}
	switch x := e.(type) {
	case *ast.CompositeLit:
		return x, sc
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return prog.literalOf(x.X, sc, depth+1)
		}
	case *ast.ParenExpr:
		return prog.literalOf(x.X, sc, depth+1)
	case *ast.StarExpr:
		return prog.literalOf(x.X, sc, depth+1)
	case *ast.Ident:
		if sc.params[x.Name] {
			return nil, sc
		}
		if sc.fn != nil {
			if expr, ok, many := localDef(sc.fn, x.Name); ok {
				if fieldsAssigned(sc.fn, x.Name) {
					return nil, sc
				}
				return prog.literalOf(expr, sc, depth+1)
			} else if many {
				return nil, sc
			}
		}
		if p := prog.pkgOf[sc.file]; p != nil {
			if expr, ok := p.decls[x.Name]; ok {
				for _, f := range p.files {
					if fieldsAssigned(f.ast, x.Name) {
						return nil, sc
					}
				}
				return prog.literalOf(expr, scope{file: p.declFile[x.Name]}, depth+1)
			}
		}
	}
	return nil, sc
}

// markDefinition marks the literal a variable starts from as seen, when the
// variable itself can't be followed, so the literal isn't reported again as
// a setting of its own.
func (prog *program) markDefinition(arg ast.Expr, sc scope, seen map[ast.Node]bool) {
	if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.AND {
		arg = u.X
	}
	id, ok := arg.(*ast.Ident)
	if !ok {
		return
	}
	var defs []ast.Expr
	if sc.fn != nil {
		defs = assignmentsTo(sc.fn, id.Name)
	}
	if p := prog.pkgOf[sc.file]; p != nil && len(defs) == 0 {
		if e, ok := p.decls[id.Name]; ok {
			defs = append(defs, e)
		}
	}
	for _, d := range defs {
		if u, ok := d.(*ast.UnaryExpr); ok && u.Op == token.AND {
			d = u.X
		}
		if lit, ok := d.(*ast.CompositeLit); ok {
			seen[lit] = true
		}
	}
}

// fieldsAssigned reports whether a field of the variable name is assigned
// or incremented anywhere under n, such as kp.Time = d. The literal the
// variable starts from then isn't the value that is used.
func fieldsAssigned(n ast.Node, name string) bool {
	found := false
	isField := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == name
	}
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, l := range n.Lhs {
				if isField(l) {
					found = true
				}
			}
		case *ast.IncDecStmt:
			if isField(n.X) {
				found = true
			}
		}
		return !found
	})
	return found
}

func (prog *program) clientFrom(arg ast.Expr, sc scope, seen map[ast.Node]bool) *clientSetting {
	lit, lsc := prog.literalOf(arg, sc, 0)
	if lit == nil {
		prog.markDefinition(arg, sc, seen)
		return &clientSetting{expr: prog.text(arg), pws: value{known: true, isB: true}}
	}
	seen[lit] = true
	return prog.clientLit(lit, lsc)
}

func (prog *program) serverFrom(arg ast.Expr, sc scope, seen map[ast.Node]bool) *serverPolicy {
	lit, lsc := prog.literalOf(arg, sc, 0)
	if lit == nil {
		prog.markDefinition(arg, sc, seen)
		return &serverPolicy{explicit: true, expr: prog.text(arg), pws: value{known: true, isB: true}}
	}
	seen[lit] = true
	return prog.serverLit(lit, lsc)
}

// fields maps a struct literal's fields by name, including positional
// literals, given the struct's field order.
func fields(lit *ast.CompositeLit, order []string) map[string]ast.Expr {
	out := map[string]ast.Expr{}
	for i, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok {
				out[id.Name] = kv.Value
			}
			continue
		}
		if i < len(order) {
			out[order[i]] = el
		}
	}
	return out
}

func (prog *program) clientLit(lit *ast.CompositeLit, sc scope) *clientSetting {
	fs := fields(lit, []string{"Time", "Timeout", "PermitWithoutStream"})
	c := &clientSetting{pws: value{known: true, isB: true}}
	if e, ok := fs["Time"]; ok {
		c.timeSet = true
		c.time = prog.eval(e, sc, 0)
		if c.time.known && c.time.isB {
			c.time = value{why: prog.text(e)}
		}
	}
	if e, ok := fs["PermitWithoutStream"]; ok {
		c.pws = prog.eval(e, sc, 0)
		if c.pws.known && !c.pws.isB {
			c.pws = value{why: prog.text(e)}
		}
	}
	return c
}

func (prog *program) serverLit(lit *ast.CompositeLit, sc scope) *serverPolicy {
	fs := fields(lit, []string{"MinTime", "PermitWithoutStream"})
	s := &serverPolicy{explicit: true, pws: value{known: true, isB: true}}
	if e, ok := fs["MinTime"]; ok {
		s.minSet = true
		s.minTime = prog.eval(e, sc, 0)
		if s.minTime.known && s.minTime.isB {
			s.minTime = value{why: prog.text(e)}
		}
	}
	if e, ok := fs["PermitWithoutStream"]; ok {
		s.pws = prog.eval(e, sc, 0)
		if s.pws.known && !s.pws.isB {
			s.pws = value{why: prog.text(e)}
		}
	}
	return s
}

func (prog *program) serverPingFrom(arg ast.Expr, sc scope, seen map[ast.Node]bool) value {
	lit, lsc := prog.literalOf(arg, sc, 0)
	if lit == nil {
		return value{why: prog.text(arg) + " (not a literal this tool can follow)"}
	}
	seen[lit] = true
	return prog.serverPingLit(lit, lsc)
}

// serverPingLit reads Time from a keepalive.ServerParameters literal.
// Unset means grpc-go's default of 2h.
func (prog *program) serverPingLit(lit *ast.CompositeLit, sc scope) value {
	fs := fields(lit, []string{"MaxConnectionIdle", "MaxConnectionAge", "MaxConnectionAgeGrace", "Time", "Timeout"})
	e, ok := fs["Time"]
	if !ok {
		return value{known: true, num: 0}
	}
	v := prog.eval(e, sc, 0)
	if v.known && v.isB {
		return value{why: prog.text(e)}
	}
	if !v.known {
		v.why = "ServerParameters.Time = " + v.why
	}
	return v
}

// pingsBeforeGoaway is how many pings it takes: the first one sets the
// server's last-ping time, and each of the next three is a strike. The
// server sends GOAWAY when strikes exceed 2 (maxPingStrikes).
const pingsBeforeGoaway = 4

var never = math.MaxInt64 * 1.0

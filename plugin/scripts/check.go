package main

import (
	"fmt"
	"go/token"
	"strings"
	"time"
)

type level int

const (
	levelNote level = iota
	levelWarn
	levelError
)

func (l level) String() string {
	switch l {
	case levelError:
		return "error"
	case levelWarn:
		return "warn"
	}
	return "note"
}

// Finding is one result. Code is stable across versions.
type Finding struct {
	Code    string   `json:"code"`
	Level   string   `json:"level"`
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
	Fix     string   `json:"fix,omitempty"`
	lvl     level
}

// assumption describes the server when none is in the input.
type assumption struct {
	minTime  float64
	pws      bool
	fromFlag bool
}

func (a assumption) policy() *serverPolicy {
	return &serverPolicy{
		minSet:  true,
		minTime: value{known: true, num: a.minTime},
		pws:     value{known: true, isB: true, b: a.pws},
	}
}

type serverResult struct {
	s      *serverPolicy
	active bool // a quiet RPC gets GOAWAY
	idle   bool // an idle connection gets GOAWAY
}

func check(m *model, assume assumption) []Finding {
	var out []Finding
	var known, unknown []*serverPolicy
	for _, s := range m.servers {
		if s.known() {
			known = append(known, s)
		} else {
			unknown = append(unknown, s)
		}
	}
	for _, s := range unknown {
		out = append(out, Finding{
			Code: "KA902", lvl: levelWarn, File: s.pos.Filename, Line: s.pos.Line,
			Message: "Server keepalive settings can't be read from the source, so clients weren't compared with this server",
			Details: []string{"Value: " + s.describeUnknown()},
			Fix:     "Check the value it gets at run time, or pass it with --server-min-time and --server-permit-without-stream and only the client code.",
		})
	}
	assumed := len(m.servers) == 0
	targets := known
	if assumed {
		targets = []*serverPolicy{assume.policy()}
	}
	for _, c := range m.clients {
		if !c.known() {
			out = append(out, Finding{
				Code: "KA901", lvl: levelWarn, File: c.pos.Filename, Line: c.pos.Line,
				Message: "Client keepalive can't be read from the source, so it wasn't checked",
				Details: []string{"Value: " + c.describeUnknown()},
				Fix:     "Check the value it gets at run time against the server's keepalive.EnforcementPolicy: Time must be at least MinTime.",
			})
			continue
		}
		t := c.effectiveTime()
		if !c.timeSet || c.time.num < minClientTime {
			msg := fmt.Sprintf("Time is %s, below grpc-go's 10s minimum, so the client pings every 10s", formatDuration(c.time.num))
			if !c.timeSet {
				msg = "Time isn't set, so grpc-go uses its 10s minimum and the client pings every 10s"
			}
			out = append(out, Finding{Code: "KA301", lvl: levelNote, File: c.pos.Filename, Line: c.pos.Line, Message: msg,
				Details: []string{"grpc.WithKeepaliveParams raises Time below 10s to 10s and logs \"Adjusting keepalive ping interval to minimum period of 10s\"."}})
		}
		if len(targets) == 0 {
			continue
		}
		var results []serverResult
		nActive, nIdle := 0, 0
		for _, s := range targets {
			r := serverResult{s: s}
			min := s.effectiveMinTime()
			if pt, _ := s.pingTime(); pt < t {
				// The server pings first, which counts as activity on
				// the client, so the client never sends a ping.
				results = append(results, r)
				continue
			}
			r.active = t < min
			if c.pws.b {
				if s.pws.b {
					r.idle = t < min
				} else {
					r.idle = t < noStreamPingInterval
				}
			}
			if r.active {
				nActive++
			}
			if r.idle {
				nIdle++
			}
			results = append(results, r)
		}
		lvl := levelError
		if assumed && !assume.fromFlag || len(unknown) > 0 {
			lvl = levelWarn
		}
		ctx := contextLines(assumed, assume, unknown)
		if nActive == len(results) {
			out = append(out, activeFinding(c, t, results, lvl, ctx))
		}
		if nIdle == len(results) {
			out = append(out, idleFinding(c, t, results, levelWarn, ctx))
		}
		if (nActive > 0 && nActive < len(results)) || (nIdle > 0 && nIdle < len(results)) {
			out = append(out, partialFinding(c, results))
		}
	}
	return out
}

func contextLines(assumed bool, a assumption, unknown []*serverPolicy) []string {
	var out []string
	if assumed && !a.fromFlag {
		out = append(out, "No gRPC server is in these files, so this assumes the server uses gRPC's default policy: MinTime 5m and no pings without RPCs. grpc-go, grpc-java and gRPC C-core all default to that. If the server sets its own, pass --server-min-time and --server-permit-without-stream.")
	}
	if assumed && a.fromFlag {
		out = append(out, "No gRPC server is in these files, so this uses the policy given with --server-min-time and --server-permit-without-stream.")
	}
	if len(unknown) > 0 {
		out = append(out, fmt.Sprintf("%d server %s that couldn't be read (KA902) weren't compared.", len(unknown), plural(len(unknown), "policy", "policies")))
	}
	return out
}

func activeFinding(c *clientSetting, t float64, results []serverResult, lvl level, ctx []string) Finding {
	min := strictest(results)
	title := fmt.Sprintf("Client pings every %s during a quiet RPC, and the server allows one ping every %s", formatDuration(t), formatDuration(min))
	if len(results) > 1 {
		title = fmt.Sprintf("Client pings every %s during a quiet RPC, more often than any of the %d servers in these files allows", formatDuration(t), len(results))
	}
	d := []string{c.describe()}
	d = append(d, serverLines(results)...)
	d = append(d, fmt.Sprintf("When an RPC gets no data from the server for %s, such as a long unary call or a quiet stream, the client pings. The server counts each ping that comes less than MinTime after the last one, and after 3 such pings it sends GOAWAY ENHANCE_YOUR_CALM \"too_many_pings\". In-flight RPCs on the connection fail with code Unavailable, about %s into the quiet period.", formatDuration(t), formatDuration(t*pingsBeforeGoaway)))
	d = append(d, doublingLine(t, min))
	d = append(d, ctx...)
	return Finding{
		Code: "KA101", lvl: lvl, File: c.pos.Filename, Line: c.pos.Line, Message: title, Details: d,
		Fix: fmt.Sprintf("Raise the client's Time to %s or more, or set keepalive.EnforcementPolicy{MinTime: %s} or lower on the server.", formatDuration(min), goDuration(t)),
	}
}

func idleFinding(c *clientSetting, t float64, results []serverResult, lvl level, ctx []string) Finding {
	d := []string{c.describe()}
	d = append(d, serverLines(results)...)
	d = append(d, fmt.Sprintf("With PermitWithoutStream true the client also pings when no RPC is running. A server with PermitWithoutStream false counts every such ping that comes within 2h of the last one, and one with PermitWithoutStream true counts pings that come sooner than MinTime. After about %s idle, the server sends GOAWAY ENHANCE_YOUR_CALM \"too_many_pings\" and closes the connection. The client reconnects, and an RPC that starts at that moment can fail with code Unavailable.", formatDuration(t*pingsBeforeGoaway)))
	d = append(d, ctx...)
	return Finding{
		Code: "KA102", lvl: lvl, File: c.pos.Filename, Line: c.pos.Line,
		Message: fmt.Sprintf("Client pings idle connections every %s, and the server closes them", formatDuration(t)),
		Details: d,
		Fix:     fmt.Sprintf("Set keepalive.EnforcementPolicy{MinTime: %s, PermitWithoutStream: true} on the server, or PermitWithoutStream: false on the client.", goDuration(t)),
	}
}

func partialFinding(c *clientSetting, results []serverResult) Finding {
	var d []string
	d = append(d, c.describe())
	for _, r := range results {
		mark := "accepts"
		switch {
		case r.active && r.idle:
			mark = "closes the connection during quiet RPCs and when idle"
		case r.active:
			mark = "closes the connection during quiet RPCs"
		case r.idle:
			mark = "closes idle connections"
		}
		d = append(d, fmt.Sprintf("%s: %s", r.s.describe(), mark))
	}
	d = append(d, "Keepalive only matters between a client and the server it dials, and the source doesn't show which one that is.")
	return Finding{
		Code: "KA103", lvl: levelNote, File: c.pos.Filename, Line: c.pos.Line,
		Message: "Client's keepalive pings are too frequent for some servers in these files, but not all",
		Details: d,
		Fix:     "If this client dials one of the servers that close the connection, raise its Time or lower that server's MinTime.",
	}
}

func serverLines(results []serverResult) []string {
	var out []string
	for _, r := range results {
		out = append(out, r.s.describe())
	}
	return out
}

func strictest(results []serverResult) float64 {
	min := 0.0
	for _, r := range results {
		if m := r.s.effectiveMinTime(); min == 0 || m < min {
			min = m
		}
	}
	return min
}

func doublingLine(t, min float64) string {
	if 2*t >= min {
		return fmt.Sprintf("grpc-go then doubles Time to %s for later connections on that channel, which the server accepts, so this happens once per channel.", formatDuration(2*t))
	}
	return fmt.Sprintf("grpc-go then doubles Time once, to %s, for later connections on that channel. That is still below %s, so it keeps happening.", formatDuration(2*t), formatDuration(min))
}

func (c *clientSetting) describe() string {
	tm := formatDuration(c.time.num)
	switch {
	case !c.timeSet:
		tm = "not set (10s)"
	case c.time.num < minClientTime:
		tm += " (raised to 10s)"
	}
	return fmt.Sprintf("Client: %s at %s, Time %s, PermitWithoutStream %t", c.via, posString(c.pos), tm, c.pws.b)
}

func (s *serverPolicy) describe() string {
	text := s.describePolicy()
	if pt, ok := s.pingTime(); ok && pt < noStreamPingInterval {
		text += fmt.Sprintf(", pings clients every %s", formatDuration(pt))
	}
	return text
}

func (s *serverPolicy) describePolicy() string {
	if s.pos.Filename == "" {
		return fmt.Sprintf("Server (assumed): MinTime %s, PermitWithoutStream %t", formatDuration(s.minTime.num), s.pws.b)
	}
	if !s.explicit {
		return fmt.Sprintf("Server: grpc.NewServer at %s with no keepalive.EnforcementPolicy: MinTime 5m (default), PermitWithoutStream false", posString(s.pos))
	}
	mt := formatDuration(s.effectiveMinTime())
	if !s.minSet || s.minTime.num == 0 {
		mt += " (not set, so the default)"
	}
	return fmt.Sprintf("Server: keepalive.EnforcementPolicy at %s: MinTime %s, PermitWithoutStream %t", posString(s.pos), mt, s.pws.b)
}

func (c *clientSetting) describeUnknown() string {
	if c.expr != "" {
		return c.expr + " (not a literal this tool can follow)"
	}
	var parts []string
	if c.timeSet && !c.time.known {
		parts = append(parts, "Time = "+c.time.why)
	}
	if !c.pws.known {
		parts = append(parts, "PermitWithoutStream = "+c.pws.why)
	}
	return strings.Join(parts, "; ")
}

func (s *serverPolicy) describeUnknown() string {
	if s.expr != "" {
		return s.expr + " (not a literal this tool can follow)"
	}
	var parts []string
	if s.minSet && !s.minTime.known {
		parts = append(parts, "MinTime = "+s.minTime.why)
	}
	if !s.pws.known {
		parts = append(parts, "PermitWithoutStream = "+s.pws.why)
	}
	for _, v := range s.pings {
		if !v.known {
			parts = append(parts, v.why)
		}
	}
	return strings.Join(parts, "; ")
}

func posString(p token.Position) string {
	return fmt.Sprintf("%s:%d", p.Filename, p.Line)
}

// goDuration writes a duration as Go source, such as 30 * time.Second.
func goDuration(ns float64) string {
	d := time.Duration(int64(ns))
	switch {
	case d%time.Hour == 0:
		return unitExpr(int64(d/time.Hour), "time.Hour")
	case d%time.Minute == 0:
		return unitExpr(int64(d/time.Minute), "time.Minute")
	case d%time.Second == 0:
		return unitExpr(int64(d/time.Second), "time.Second")
	case d%time.Millisecond == 0:
		return unitExpr(int64(d/time.Millisecond), "time.Millisecond")
	}
	return fmt.Sprintf("time.Duration(%d)", int64(d))
}

func unitExpr(n int64, unit string) string {
	if n == 1 {
		return unit
	}
	return fmt.Sprintf("%d * %s", n, unit)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

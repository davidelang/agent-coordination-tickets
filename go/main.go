// Channel commands. One binary. The name it is invoked as selects the mode.
// channel prints help. channel-setup, channel-arm, and channel-send are links to it.
// --setup, --arm, and --send select the mode when the name is channel.
//
// Standard library only. Build with CGO_ENABLED=0 and -ldflags '-s -w'.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/syslog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const poll = 200 * time.Millisecond

var (
	ops    = []string{"<->", ">-<", "<-", "->", "--", "-<", ">-"}
	nameRE = regexp.MustCompile(`^(\d{8}-\d{6}\.\d{3})__(.+)__(\d+)\.md$`)
	nodeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
	msgRE  = regexp.MustCompile(`(?m)^[ \t]*message[ \t]+([A-Za-z][A-Za-z0-9_.-]*)[ \t]*\{`)
	wordRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_.-]*`)
)

type edge struct{ src, dst, mode, label string }
type message struct {
	desc string
	body []string
}
type plan struct {
	path      string
	nodes     map[string]string
	nodeOrder []string
	verbs     map[string][]string
	verbOrder [][2]string
	messages  map[string]message
	edges     []edge
	hooks     map[string]hook
}

// Optional per-node webhook: node NAME PATH webhook=URL [webhook_key=TOKEN|webhook_key_file=PATH (relative: diag dir)]
type hook struct {
	url, keyFile, key   string
	retries, backoffMax int
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

// quiet hides stdout lines while channel-arm answers a PING, so the agent is not woken.
var quiet bool

func say(line string) {
	if !quiet {
		fmt.Println(line)
	}
}

func note(msg string) { fmt.Fprintln(os.Stderr, msg) }

func help() {
	fmt.Print(`channel — one inbox directory per agent

With no mode, this prints help and exits.

  channel --setup FILE NODE [-n|--dry-run] [--whatineedtoknow]
  channel --arm   FILE NODE [--whatineedtoknow]
  channel --send  FILE NODE DEST VERB [BODYFILE|-|-m TEXT ...] [REF ...] [--whatineedtoknow]
  channel --ping  FILE NODE [--force] [--every MIN] [--timeout SEC]
  channel --status FILE

channel-setup, channel-arm, and channel-send are names for this same program.
The name selects the mode, so those forms do not need --setup, --arm, or --send.

-n, --dry-run       check the plan and create nothing (setup only)
--whatineedtoknow   print how to act (paths, watch/send commands, body rules), then
                    the nodes and messages this node uses, then exit
-h, --help          print this help

Options may sit before or after the file and the node. -- ends the options.

Webhooks (optional, per node, workflow-specific: they live only in that diag):
  node NAME /abs/inbox webhook=URL [webhook_key=TOKEN | webhook_key_file=PATH]
A relative webhook_key_file is resolved from the diag file's directory.
Give only the key: the tool sends Authorization: Bearer KEY. A leading
"Authorization: Bearer " or "Bearer " is stripped (key file: first line).
After channel-send queues a message to NAME, it POSTs JSON
  {"target","from","verb","inbox","file"}
to URL with Content-Type: application/json.
Optional webhook_retries=N (default 3) and webhook_backoff_max=SEC (default 30):
network errors, 5xx and 429 are retried with exponential backoff.
Success prints "WEBHOOK NAME CODE RESPONSE". Any failure prints a WARNING on
stderr; the message stays queued and the exit status is still 0.

PING and PONG (body NONCE: <value>) are built in on every arrow; channel-arm
answers PING itself. Events go to syslog, tag agent-channel, as key=value
(CHANNEL_SYSLOG=0 disables; CHANNEL_LOG_FILE=PATH also appends to a file).
`)
}

func main() {
	nameMode := modeFromName(os.Args[0])
	opts, rest, err := gnuGetopt(os.Args[1:], "hnm:", []string{
		"help", "dry-run", "whatineedtoknow", "setup", "arm", "send", "ping", "status",
		"force", "every=", "timeout=",
	})
	if err != nil {
		die(err.Error())
	}
	dry, what, helpFlag := false, false, false
	flagMode := ""
	var msgs []string
	for _, opt := range opts {
		switch {
		case strings.HasPrefix(opt, "-m="):
			msgs = append(msgs, opt[3:])
			continue
		case opt == "--force":
			pingForce = true
			continue
		case strings.HasPrefix(opt, "--every="):
			v, err := strconv.Atoi(opt[8:])
			if err != nil || v < 0 {
				die("--every needs whole minutes")
			}
			pingEvery = v
			continue
		case strings.HasPrefix(opt, "--timeout="):
			v, err := strconv.Atoi(opt[10:])
			if err != nil || v <= 0 {
				die("--timeout needs whole seconds")
			}
			pingTimeout = v
			continue
		}
		switch opt {
		case "-h", "--help":
			helpFlag = true
		case "-n", "--dry-run":
			dry = true
		case "--whatineedtoknow":
			what = true
		case "--setup", "--arm", "--send", "--ping", "--status":
			m := strings.TrimPrefix(opt, "--")
			if flagMode != "" && flagMode != m {
				die("pick one of --setup, --arm, --send, --ping, or --status")
			}
			flagMode = m
		}
	}
	if helpFlag {
		help()
		return
	}
	if nameMode != "" && flagMode != "" && nameMode != flagMode {
		die("this command is " + nameMode + "; " + flagMode + " is a different mode")
	}
	mode := nameMode
	if flagMode != "" {
		mode = flagMode
	}
	if mode == "" {
		if dry || what || len(rest) > 0 {
			die("no mode; use --setup, --arm, --send, --ping, or --status")
		}
		help()
		return
	}
	invoked := "channel-" + mode
	if nameMode == "" {
		invoked = "channel --" + mode
	}
	if len(msgs) > 0 && mode != "send" {
		die("-m is only for channel-send")
	}
	switch mode {
	case "ping":
		if dry || what || len(rest) != 2 {
			die("usage: " + invoked + " FILE NODE [--force] [--every MIN] [--timeout SEC]")
		}
		cmdPing(rest[0], rest[1])
		return
	case "status":
		if dry || what || len(rest) != 1 {
			die("usage: " + invoked + " FILE")
		}
		cmdStatus(rest[0])
		return
	case "setup":
		if len(rest) != 2 {
			die("usage: " + invoked + " FILE NODE [-n|--dry-run] [--whatineedtoknow]")
		}
		cmdSetup(rest[0], rest[1], dry, what)
	case "arm":
		if dry {
			die("--dry-run is only for channel-setup")
		}
		if what {
			if len(rest) != 2 {
				die("usage: " + invoked + " FILE NODE --whatineedtoknow")
			}
			showWhat(rest[0], rest[1])
			return
		}
		if len(rest) != 2 {
			die("usage: " + invoked + " FILE NODE")
		}
		cmdArm(rest[0], rest[1])
	case "send":
		if dry {
			die("--dry-run is only for channel-setup")
		}
		if what {
			if len(rest) != 2 {
				die("usage: " + invoked + " FILE NODE --whatineedtoknow")
			}
			showWhat(rest[0], rest[1])
			return
		}
		if len(rest) < 4 {
			die("usage: " + invoked + " FILE NODE RECIPIENT VERB [BODYFILE|-|-m TEXT...] [REF ...]")
		}
		var bodyText []byte
		var refs []string
		switch {
		case len(msgs) > 0:
			bodyText = []byte(strings.Join(msgs, "\n\n") + "\n")
			refs = rest[4:]
		case len(rest) == 4 || rest[4] == "-":
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				die("cannot read the body from stdin")
			}
			bodyText = b
			if len(rest) > 4 {
				refs = rest[5:]
			}
		default:
			b, err := os.ReadFile(rest[4])
			if err != nil {
				die("no body file " + rest[4])
			}
			bodyText = b
			refs = rest[5:]
		}
		cmdSend(rest[0], rest[1], rest[2], rest[3], bodyText, refs)
	default:
		die("unknown mode " + mode)
	}
}

func modeFromName(argv0 string) string {
	base := filepath.Base(argv0)
	switch base {
	case "channel-setup":
		return "setup"
	case "channel-arm":
		return "arm"
	case "channel-send":
		return "send"
	case "channel-ping":
		return "ping"
	case "channel-status":
		return "status"
	default:
		return ""
	}
}

func gnuGetopt(args []string, short string, long []string) ([]string, []string, error) {
	var opts, rest []string
	knownShort := map[byte]bool{}
	for i := 0; i < len(short); i++ {
		knownShort[short[i]] = true
	}
	knownLong := map[string]bool{}
	for _, name := range long {
		knownLong[name] = true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			continue
		}
		if strings.HasPrefix(a, "--") {
			name := strings.TrimPrefix(a, "--")
			val, hasVal := "", false
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name, val, hasVal = name[:eq], name[eq+1:], true
			}
			if knownLong[name+"="] {
				if !hasVal {
					if i+1 >= len(args) {
						return nil, nil, fmt.Errorf("option --%s needs a value", name)
					}
					i++
					val = args[i]
				}
				opts = append(opts, "--"+name+"="+val)
				continue
			}
			if hasVal {
				return nil, nil, fmt.Errorf("option --%s must not have a value", name)
			}
			if !knownLong[name] {
				return nil, nil, fmt.Errorf("option --%s not recognized", name)
			}
			opts = append(opts, "--"+name)
			continue
		}
		for j := 1; j < len(a); j++ {
			c := a[j]
			if !knownShort[c] {
				return nil, nil, fmt.Errorf("option -%c not recognized", c)
			}
			{
				if k := strings.IndexByte(short, c); k+1 < len(short) && short[k+1] == ':' {
					val := a[j+1:]
					if val == "" {
						if i+1 >= len(args) {
							return nil, nil, fmt.Errorf("option -%c needs a value", c)
						}
						i++
						val = args[i]
					}
					opts = append(opts, "-"+string(c)+"="+val)
					break
				}
			}
			opts = append(opts, "-"+string(c))
		}
	}
	return opts, rest, nil
}

func validNode(name string) bool {
	return nodeRE.MatchString(name) && !strings.Contains(name, "__") && name != "blockdiag" && name != "group"
}

func loadPlan(path string) plan {
	textb, err := os.ReadFile(path)
	if err != nil {
		die("no plan file " + path)
	}
	text := string(textb)
	rawEdges := parseDiag(text)
	edges := expand(rawEdges)
	tail := afterDiagram(text)
	base := strings.Count(text[:len(text)-len(tail)], "\n")
	p := parseKey(tail, base)
	p.path = path
	var problems []string
	seen := map[string]bool{}
	for _, e := range edges {
		if _, ok := p.nodes[e.src]; !ok {
			problems = append(problems, e.src+" -> "+e.dst+" uses a node the plan file does not define")
		} else if _, ok := p.nodes[e.dst]; !ok {
			problems = append(problems, e.src+" -> "+e.dst+" uses a node the plan file does not define")
		}
		key := e.src + "\x00" + e.dst
		if _, ok := p.verbs[key]; !ok {
			problems = append(problems, e.src+" -> "+e.dst+" has no verb line")
		}
		if seen[key] {
			problems = append(problems, "duplicate edge "+e.src+" -> "+e.dst)
		}
		seen[key] = true
	}
	inEdge := map[string]bool{}
	for key := range seen {
		parts := strings.SplitN(key, "\x00", 2)
		inEdge[parts[0]] = true
		inEdge[parts[1]] = true
	}
	for _, name := range p.nodeOrder {
		if !inEdge[name] {
			problems = append(problems, "node "+name+" is in the key and not in the diagram")
		}
	}
	for _, pair := range p.verbOrder {
		key := pair[0] + "\x00" + pair[1]
		if !seen[key] {
			problems = append(problems, "verb "+pair[0]+" "+pair[1]+" is not an arrow in the diagram")
		}
		for _, word := range p.verbs[key] {
			if _, ok := p.messages[word]; !ok {
				problems = append(problems, word+" on "+pair[0]+" -> "+pair[1]+" has no message definition")
			}
		}
	}
	if len(problems) > 0 {
		die(strings.Join(problems, "\n"))
	}
	p.edges = edges
	return p
}

func stripComments(text string) string {
	var b strings.Builder
	i, n := 0, len(text)
	for i < n {
		if strings.HasPrefix(text[i:], "/*") {
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				break
			}
			i += 2 + end + 2
			continue
		}
		if strings.HasPrefix(text[i:], "//") || text[i] == '#' {
			nl := strings.IndexByte(text[i:], '\n')
			if nl < 0 {
				break
			}
			i += nl + 1
			b.WriteByte('\n')
			continue
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

func parseDiag(text string) [][4]string {
	body := stripComments(text)
	var edges [][4]string
	for _, stmt := range strings.Split(body, ";") {
		label := ""
		if open := strings.IndexByte(stmt, '['); open >= 0 {
			close := strings.LastIndexByte(stmt, ']')
			if close > open {
				label = labelFrom(stmt[open+1 : close])
				stmt = stmt[:open] + stmt[close+1:]
			}
		}
		opsFound, parts := splitOps(stmt)
		if len(opsFound) == 0 {
			if strings.ContainsAny(stmt, "<>") {
				die("unrecognized arrow in: " + strings.TrimSpace(stmt) + ". Use ->, <->, or -<.")
			}
			continue
		}
		rest := stmt
		for _, op := range opsFound {
			rest = strings.Replace(rest, op, " ", 1)
		}
		if strings.ContainsAny(rest, "<>") {
			die("unrecognized arrow in: " + strings.TrimSpace(stmt) + ". Use ->, <->, or -<.")
		}
		var nodes []string
		for index, part := range parts {
			names := wordRE.FindAllString(part, -1)
			var kept []string
			for _, name := range names {
				if name != "blockdiag" && name != "group" {
					kept = append(kept, name)
				}
			}
			if len(kept) == 0 {
				continue
			}
			if index == 0 {
				nodes = append(nodes, kept[len(kept)-1])
			} else {
				nodes = append(nodes, kept[0])
			}
		}
		if len(nodes) != len(opsFound)+1 {
			die("could not read an edge statement: " + strings.TrimSpace(stmt))
		}
		for i, op := range opsFound {
			edges = append(edges, [4]string{nodes[i], op, nodes[i+1], label})
		}
	}
	return edges
}

func labelFrom(attr string) string {
	i := strings.Index(attr, "label")
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(attr[i+len("label"):])
	if !strings.HasPrefix(rest, "=") {
		return ""
	}
	rest = strings.TrimSpace(rest[1:])
	if rest == "" {
		return ""
	}
	if rest[0] == '"' || rest[0] == '\'' {
		q := rest[0]
		end := strings.IndexByte(rest[1:], q)
		if end < 0 {
			return ""
		}
		return rest[1 : 1+end]
	}
	end := 0
	for end < len(rest) && isLabelChar(rest[end]) {
		end++
	}
	return rest[:end]
}

func isLabelChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-'
}

func splitOps(stmt string) ([]string, []string) {
	var found, parts []string
	start := 0
	for i := 0; i < len(stmt); {
		matched := ""
		for _, op := range ops {
			if strings.HasPrefix(stmt[i:], op) {
				matched = op
				break
			}
		}
		if matched == "" {
			i++
			continue
		}
		parts = append(parts, stmt[start:i])
		found = append(found, matched)
		i += len(matched)
		start = i
	}
	parts = append(parts, stmt[start:])
	return found, parts
}

func expand(raw [][4]string) []edge {
	var out []edge
	for _, item := range raw {
		src, op, dst, label := item[0], item[1], item[2], item[3]
		switch op {
		case "<->":
			out = append(out, edge{src, dst, "wait", label}, edge{dst, src, "wait", label})
		case "->":
			out = append(out, edge{src, dst, "wait", label})
		case "-<":
			out = append(out, edge{src, dst, "queue", label})
		case "<-", "--", ">-", ">-<":
			die(op + " is drawn by blockdiag and is not a channel. Use ->, <->, or -<. See docs/diagram-marks.md.")
		default:
			die("unknown operator " + op)
		}
	}
	return out
}

func afterDiagram(text string) string {
	depth, started := 0, false
	i, n := 0, len(text)
	for i < n {
		if strings.HasPrefix(text[i:], "/*") {
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				die("unclosed block comment in plan file")
			}
			i += 2 + end + 2
			continue
		}
		if text[i] == '#' || strings.HasPrefix(text[i:], "//") {
			nl := strings.IndexByte(text[i:], '\n')
			if nl < 0 {
				i = n
			} else {
				i += nl + 1
			}
			continue
		}
		if text[i] == '"' || text[i] == '\'' {
			q := text[i]
			i++
			for i < n && text[i] != q {
				if text[i] == '\\' {
					i += 2
					continue
				}
				i++
			}
			i++
			continue
		}
		if text[i] == '{' {
			depth++
			started = true
		} else if text[i] == '}' {
			depth--
			if started && depth == 0 {
				return text[i+1:]
			}
		}
		i++
	}
	die("plan file has no closed blockdiag diagram")
	return ""
}

func matchingBrace(text string, openAt int) int {
	depth := 0
	for i := openAt; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func parseKey(text string, lineBase int) plan {
	p := plan{
		nodes:    map[string]string{},
		verbs:    map[string][]string{},
		messages: map[string]message{},
		hooks:    map[string]hook{},
	}
	blank := []byte(text)
	cursor := 0
	for {
		loc := msgRE.FindStringSubmatchIndex(text[cursor:])
		if loc == nil {
			break
		}
		abs := cursor + loc[0]
		name := text[cursor+loc[2] : cursor+loc[3]]
		openAt := cursor + loc[1] - 1
		closeAt := matchingBrace(text, openAt)
		if closeAt < 0 {
			die("message " + name + " is missing its closing brace")
		}
		if _, ok := p.messages[name]; ok {
			die("message " + name + " is defined twice")
		}
		p.messages[name] = splitMessage(name, text[openAt+1:closeAt])
		for i := abs; i <= closeAt; i++ {
			if blank[i] != '\n' {
				blank[i] = ' '
			}
		}
		cursor = closeAt + 1
	}
	for lineno, raw := range strings.Split(string(blank), "\n") {
		line := strings.TrimSpace(raw)
		n := lineBase + lineno + 1
		if line == "" || line == "/*" || line == "*/" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		parts := strings.Fields(line)
		if parts[0] == "node" && len(parts) >= 3 {
			name, inbox := parts[1], parts[2]
			h := hook{retries: 3, backoffMax: 30}
			for _, kv := range parts[3:] {
				eq := strings.Index(kv, "=")
				if eq < 1 {
					die(fmt.Sprintf("line %d: node option must be key=value (%s)", n, kv))
				}
				key, val := kv[:eq], kv[eq+1:]
				if val == "" {
					die(fmt.Sprintf("line %d: %s is empty", n, key))
				}
				switch key {
				case "webhook":
					if !strings.HasPrefix(val, "http://") && !strings.HasPrefix(val, "https://") {
						die(fmt.Sprintf("line %d: webhook must start with http:// or https://", n))
					}
					h.url = val
				case "webhook_key_file":
					h.keyFile = val
				case "webhook_key":
					h.key = val
				case "webhook_retries", "webhook_backoff_max":
					v, err := strconv.Atoi(val)
					if err != nil || v < 0 {
						die(fmt.Sprintf("line %d: %s must be a whole number >= 0", n, key))
					}
					if key == "webhook_retries" {
						h.retries = v
					} else {
						h.backoffMax = v
					}
				default:
					die(fmt.Sprintf("line %d: unknown node option %s (allowed: webhook, webhook_key_file, webhook_key, webhook_retries, webhook_backoff_max)", n, key))
				}
			}
			if h.keyFile != "" && h.key != "" {
				die(fmt.Sprintf("line %d: use webhook_key_file or webhook_key, not both", n))
			}
			if h.url == "" && (h.keyFile != "" || h.key != "") {
				die(fmt.Sprintf("line %d: webhook key given without webhook=URL", n))
			}
			if !validNode(name) {
				die(fmt.Sprintf("line %d: node name must be letters, digits, dot, underscore, or hyphen, and not contain __", n))
			}
			if _, ok := p.nodes[name]; ok {
				die(fmt.Sprintf("line %d: node %s is listed twice", n, name))
			}
			if !filepath.IsAbs(inbox) {
				die(fmt.Sprintf("line %d: inbox path must be absolute", n))
			}
			p.nodes[name] = inbox
			if h.url != "" {
				p.hooks[name] = h
			}
			p.nodeOrder = append(p.nodeOrder, name)
		} else if parts[0] == "verb" && len(parts) >= 4 {
			src, dst, words := parts[1], parts[2], parts[3:]
			key := src + "\x00" + dst
			if _, ok := p.verbs[key]; ok {
				die(fmt.Sprintf("line %d: verb %s %s is listed twice", n, src, dst))
			}
			p.verbs[key] = append([]string{}, words...)
			p.verbOrder = append(p.verbOrder, [2]string{src, dst})
		} else {
			die(fmt.Sprintf("line %d: expected 'node NAME PATH [webhook=URL [webhook_key_file=PATH|webhook_key=TOKEN]]', 'verb SRC DST WORD...', or 'message NAME { ... }'", n))
		}
	}
	return p
}

func splitMessage(name, interior string) message {
	idx := regexp.MustCompile(`body[ \t]*:[ \t]*\{`).FindStringIndex(interior)
	if idx == nil {
		die("message " + name + " has no body")
	}
	openAt := idx[1] - 1
	closeAt := matchingBrace(interior, openAt)
	if closeAt < 0 {
		die("message " + name + " body is missing its closing brace")
	}
	var body []string
	for _, line := range strings.Split(interior[openAt+1:closeAt], "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			body = append(body, line)
		}
	}
	if len(body) == 0 {
		die("message " + name + " has an empty body")
	}
	desc := strings.TrimSpace(interior[:idx[0]] + interior[closeAt+1:])
	return message{desc: desc, body: body}
}

func requireMe(me string, p plan) {
	if !validNode(me) {
		die("node must be a bare name, not a path (" + me + ")")
	}
	if _, ok := p.nodes[me]; !ok {
		die(me + " is not a node in " + p.path)
	}
}

func reportEdges(p plan) {
	abs, err := filepath.Abs(p.path)
	if err != nil {
		abs = p.path
	}
	say("OK " + abs)
	for _, e := range p.edges {
		extra := ""
		if e.label != "" {
			extra = " " + e.label
		}
		say(e.src + " -> " + e.dst + " " + e.mode + extra)
	}
}

func emitAct(me string, p plan) {
	plan, err := filepath.Abs(p.path)
	if err != nil {
		plan = p.path
	}
	inbox, err := filepath.Abs(p.nodes[me])
	if err != nil {
		inbox = p.nodes[me]
	}
	bin := ""
	if exe, err := os.Executable(); err == nil {
		bin = filepath.Dir(exe)
	}
	r := strings.NewReplacer("{NODE}", me, "{PLAN}", plan, "{INBOX}", inbox, "{BIN}", bin)
	for _, line := range []string{
		"how to act:",
		"you are node {NODE}",
		"plan: {PLAN}",
		"inbox: {INBOX}",
		"watch: {BIN}/channel-arm {PLAN} {NODE}",
		"  Leave the watch running as a persistent monitor. It prints READY, then FILE <path> for the oldest message in your inbox, and the next oldest only after that file is removed.",
		"  The watch answers PING for you (it sends PONG and removes the PING) and never shows it. With no watch running, answer a PING yourself: send PONG to its sender with the same NONCE line, then rm the PING.",
		"start: before new messages, look in {INBOX}/claimed/. Those are messages you already started and may have partly done. Check whether your reply was already sent before redoing anything. Nothing reclaims a claim by age; report claims you cannot finish.",
		"read: a message file has FROM, TO, SEQ, VERB and REFS (zero or more files, paths relative to the plan directory, or - none), then a --- line, then the body.",
		"  Handle the oldest FILE first, one file at a time. You may move it into {INBOX}/claimed/ while you work (the watch ignores subfolders and shows the next file); optionally write claimed/<file>.owner saying who has it.",
		"send: {BIN}/channel-send {PLAN} {NODE} DEST VERB BODYFILE [REF ...]",
		"  or:  {BIN}/channel-send {PLAN} {NODE} DEST VERB -m 'TEXT' [-m 'TEXT' ...] [REF ...]   (each -m is one block of lines; blocks are joined by a blank line)",
		"  or:  {BIN}/channel-send {PLAN} {NODE} DEST VERB - [REF ...]   (- or no BODYFILE reads the body from stdin)",
		"  DEST must be a node you have an arrow to and VERB must be listed on that arrow (see the verb lines below). PING and PONG are built in on every arrow; their body is NONCE: <value>.",
		"  Body: copy every line without <...> exactly. For a line with <...>, keep the text before < and replace <...> with a real value.",
		"  Lines are compared after trimming spaces; blank lines are ignored and extra lines are allowed.",
		"  REF files are optional and a message may carry several; each is copied into refs/ and listed under REFS.",
		"  On a -> arrow the send waits until DEST removes your previous message; on -< it queues. PING and PONG never wait.",
		"done: rm the FILE when you have finished it (from claimed/ only after your reply is sent). Removing it is the acknowledgement.",
		"tools: {BIN}/channel-ping {PLAN} {NODE} tests each of your arrows (skipped if run in the last 30 minutes; --force, --every MIN).",
		"  {BIN}/channel-status {PLAN} shows each inbox's message count, oldest age, and claimed files.",
		"after a start or a compact, run {BIN}/channel-arm {PLAN} {NODE} --whatineedtoknow again, then check claimed/ (see start).",
		"",
	} {
		say(r.Replace(line))
	}
}

func emitWhat(me string, p plan) {
	emitAct(me, p)
	related := map[string]bool{me: true}
	var touching []edge
	for _, e := range p.edges {
		if e.src != me && e.dst != me {
			continue
		}
		related[e.src] = true
		related[e.dst] = true
		touching = append(touching, e)
	}
	say("node " + me + " " + p.nodes[me])
	var others []string
	for name := range related {
		if name != me {
			others = append(others, name)
		}
	}
	sort.Strings(others)
	for _, name := range others {
		say("node " + name + " " + p.nodes[name])
	}
	var used []string
	for _, e := range touching {
		say(e.src + " -> " + e.dst + " " + e.mode)
		words := p.verbs[e.src+"\x00"+e.dst]
		say("verb " + e.src + " " + e.dst + " " + strings.Join(words, " "))
		used = append(used, words...)
	}
	seen := map[string]bool{}
	for _, word := range used {
		if seen[word] {
			continue
		}
		seen[word] = true
		msg := p.messages[word]
		say("message " + word + " {")
		if msg.desc != "" {
			say(msg.desc)
		}
		say("body: {")
		for _, line := range msg.body {
			say(line)
		}
		say("}")
		say("}")
	}
}

func showWhat(path, me string) {
	p := loadPlan(path)
	requireMe(me, p)
	emitWhat(me, p)
}

func cmdSetup(path, me string, dry, what bool) {
	p := loadPlan(path)
	requireMe(me, p)
	if dry {
		reportEdges(p)
		say("dry-run")
	}
	if what {
		emitWhat(me, p)
	}
	if dry || what {
		return
	}
	old := syscall.Umask(0o002)
	defer syscall.Umask(old)
	// Rerunning on an existing node is safe: create whatever is missing, change nothing that exists.
	refs := filepath.Join(filepath.Dir(p.path), "refs")
	if info, err := os.Stat(refs); err != nil || !info.IsDir() {
		if err := os.MkdirAll(refs, 0o775); err != nil {
			die(err.Error())
		}
		tryChmod(refs, os.ModeSetgid|0o775)
	}
	inbox := p.nodes[me]
	if info, err := os.Stat(inbox); err == nil && info.IsDir() {
		say("node " + me + " inbox " + inbox + " existing")
		return
	}
	if err := os.MkdirAll(inbox, 0o775); err != nil {
		die(err.Error())
	}
	node := filepath.Dir(inbox)
	tryChmod(node, os.ModeSetgid|0o775)
	tryChmod(inbox, os.ModeSetgid|0o775)
	say("node " + me + " inbox " + inbox)
}

func tryChmod(path string, mode os.FileMode) {
	if err := os.Chmod(path, mode); err != nil {
		note(fmt.Sprintf("chmod %#o %s failed: %s", mode, path, err))
	}
}

func requireDir(path, hint string) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		die("no directory " + path + "; " + hint)
	}
}

func messageNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var found []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if nameRE.MatchString(entry.Name()) {
			found = append(found, entry.Name())
		}
	}
	sort.Strings(found)
	return found
}

func cmdArm(path, me string) {
	p := loadPlan(path)
	requireMe(me, p)
	armPlan, armMe, armActive = p, me, true
	inbox := p.nodes[me]
	requireDir(inbox, "run channel-setup "+path+" "+me)
	proc := tryInotify([]string{
		"inotifywait", "-m", "-q",
		"-e", "moved_to", "-e", "moved_from", "-e", "delete",
		"--format", "%f", inbox,
	})
	if proc == nil {
		note("channel-arm is listing the inbox every 0.2s. inotifywait is not in use.")
	}
	say("READY")
	shown := announce(inbox, "")
	if proc == nil {
		watchOldest(inbox, shown)
		return
	}
	sc := bufio.NewScanner(proc)
	for sc.Scan() {
		shown = announce(inbox, shown)
	}
	stopProc(proc)
	note("inotifywait ended; listing the inbox from here on.")
	watchOldest(inbox, shown)
}

func announce(inbox, shown string) string {
	info, err := os.Stat(inbox)
	if err != nil || !info.IsDir() {
		die("inbox disappeared: " + inbox)
	}
	files := handleControl(inbox, messageNames(inbox))
	oldest := ""
	if len(files) > 0 {
		oldest = files[0]
	}
	if oldest == shown {
		return shown
	}
	if shown != "" && fileExists(filepath.Join(inbox, shown)) {
		return shown
	}
	if shown != "" {
		logEvent("consumed", "node", armMe, "file", filepath.Join(inbox, shown))
	}
	if oldest == "" {
		return ""
	}
	say("FILE " + filepath.Join(inbox, oldest))
	logEvent("deliver", "node", armMe, "pending", strconv.Itoa(len(files)), "file", filepath.Join(inbox, oldest))
	return oldest
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func watchOldest(inbox, shown string) {
	for {
		shown = announce(inbox, shown)
		time.Sleep(poll)
	}
}

func usePoll() bool { return os.Getenv("CHANNEL_POLL") == "1" }

func tryInotify(args []string) io.Reader {
	if usePoll() {
		return nil
	}
	if _, err := exec.LookPath(args[0]); err != nil {
		return nil
	}
	cmd := exec.Command(args[0], args[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil
	}
	time.Sleep(50 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		_ = cmd.Wait()
		return nil
	}
	return &held{Reader: stdout, cmd: cmd}
}

type held struct {
	io.Reader
	cmd *exec.Cmd
}

func stopProc(r io.Reader) {
	h, ok := r.(*held)
	if !ok || h == nil || h.cmd == nil || h.cmd.Process == nil {
		return
	}
	_ = h.cmd.Process.Signal(syscall.SIGTERM)
	_ = h.cmd.Wait()
}

func waitUntilRemoved(inbox, name string) {
	target := filepath.Join(inbox, name)
	say("PENDING " + target)
	proc := tryInotify([]string{
		"inotifywait", "-m", "-q",
		"-e", "moved_from", "-e", "delete",
		"--format", "%f", inbox,
	})
	if proc == nil {
		note("channel-send is listing the inbox every 0.2s until the pending file is removed. inotifywait is not in use.")
		for fileExists(target) {
			time.Sleep(poll)
		}
		return
	}
	defer stopProc(proc)
	if !fileExists(target) {
		return
	}
	sc := bufio.NewScanner(proc)
	for sc.Scan() {
		if !fileExists(target) {
			return
		}
	}
	if fileExists(target) {
		note("inotifywait ended; listing the inbox until the pending file is removed.")
		for fileExists(target) {
			time.Sleep(poll)
		}
	}
}

func checkBody(verb string, template []string, text string) {
	var sent []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			sent = append(sent, line)
		}
	}
	for _, raw := range template {
		open := strings.IndexByte(raw, '<')
		close := strings.LastIndexByte(raw, '>')
		if open >= 0 && open < close {
			prefix := raw[:open]
			filled := false
			for _, line := range sent {
				if prefix != "" && !strings.HasPrefix(line, prefix) {
					continue
				}
				value := line
				if prefix != "" {
					value = strings.TrimSpace(line[len(prefix):])
				}
				if value != "" && !(strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">")) {
					filled = true
					break
				}
			}
			if !filled {
				die(verb + " body is missing a value for: " + raw)
			}
		} else if !containsLine(sent, raw) {
			die(verb + " body is missing the line: " + raw)
		}
	}
}

func containsLine(lines []string, raw string) bool {
	for _, line := range lines {
		if line == raw {
			return true
		}
	}
	return false
}

func senderOf(name string) string {
	m := nameRE.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return m[2]
}

func cmdSend(path, me, recipient, verb string, bodyText []byte, refs []string) {
	p := loadPlan(path)
	requireMe(me, p)
	sendMessage(p, me, recipient, verb, bodyText, refs)
}

func isControl(verb string) bool { return verb == "PING" || verb == "PONG" }

// A PING or PONG is a control message (never waits, handled by channel-arm) only when its body has a NONCE line.
func hasNonce(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "NONCE:") && strings.TrimSpace(line[6:]) != "" {
			return true
		}
	}
	return false
}

func sendMessage(p plan, me, recipient, verb string, bodyText []byte, refs []string) string {
	path := p.path
	if _, ok := p.nodes[recipient]; !ok {
		die("unknown node (recipient=" + recipient + ")")
	}
	var found *edge
	for i := range p.edges {
		if p.edges[i].src == me && p.edges[i].dst == recipient {
			found = &p.edges[i]
			break
		}
	}
	if found == nil {
		die(me + " is not allowed to send to " + recipient)
	}
	words := p.verbs[me+"\x00"+recipient]
	if !containsLine(words, verb) && !isControl(verb) {
		die("verb " + verb + " is not allowed on " + me + " -> " + recipient)
	}
	template := p.messages[verb].body
	if isControl(verb) && len(template) == 0 {
		template = []string{"NONCE: <nonce>"}
	}
	checkBody(verb, template, string(bodyText))
	for _, ref := range refs {
		if !fileExists(ref) {
			die("no referenced file " + ref)
		}
	}
	requireDir(p.nodes[me], "run channel-setup "+p.path+" "+me)
	inbox := p.nodes[recipient]
	requireDir(inbox, "run channel-setup "+p.path+" "+recipient)
	if found.mode == "wait" && !(isControl(verb) && hasNonce(string(bodyText))) {
		for _, name := range messageNames(inbox) {
			if senderOf(name) == me {
				waitUntilRemoved(inbox, name)
			}
		}
	}
	for _, ref := range refs {
		if !fileExists(ref) {
			die("no referenced file " + ref)
		}
	}
	seqPath := filepath.Join(filepath.Dir(p.nodes[me]), "out-seq.txt")
	current := nextSeq(seqPath)
	now := time.Now()
	stamp := now.Format("20060102-150405") + fmt.Sprintf(".%03d", now.Nanosecond()/1e6)
	refsDir := filepath.Join(filepath.Dir(p.path), "refs")
	requireDir(refsDir, "run channel-setup "+p.path+" "+me)
	var copied []string
	for _, ref := range refs {
		dest := filepath.Join(refsDir, fmt.Sprintf("%s__%s__%04d__%s", stamp, me, current, filepath.Base(ref)))
		if err := copyFile(ref, dest); err != nil {
			die(err.Error())
		}
		tryChmod(dest, 0o660)
		rel, err := filepath.Rel(filepath.Dir(p.path), dest)
		if err != nil {
			die(err.Error())
		}
		copied = append(copied, filepath.ToSlash(rel))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FROM: %s\nTO: %s\nSEQ: %d\nVERB: %s\nREFS:\n", me, recipient, current, verb)
	if len(copied) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, item := range copied {
			b.WriteString("- " + item + "\n")
		}
	}
	b.WriteString("---\n")
	b.Write(bodyText)
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
	filename := fmt.Sprintf("%s__%s__%04d.md", stamp, me, current)
	tmp := filepath.Join(filepath.Dir(p.nodes[me]), ".tmp-"+filename)
	if err := os.WriteFile(tmp, []byte(b.String()), 0o660); err != nil {
		die(err.Error())
	}
	tryChmod(tmp, 0o660)
	dest := filepath.Join(inbox, filename)
	publishReplace(tmp, dest)
	say("SENT " + dest)
	logEvent("send", "plan", path, "from", me, "to", recipient, "verb", verb, "seq", strconv.Itoa(current), "refs", strconv.Itoa(len(copied)), "file", dest)
	fireWebhook(p, recipient, me, verb, inbox, dest)
	return dest
}

// fireWebhook POSTs after a successful send. Failures only warn; the message stays queued.
func fireWebhook(p plan, target, from, verb, inbox, file string) {
	h, ok := p.hooks[target]
	if !ok {
		return
	}
	warn := func(why string) {
		note("WARNING: webhook for " + target + " " + why + " (message is queued)")
	}
	key := h.key
	if h.keyFile != "" {
		kf := h.keyFile
		if !filepath.IsAbs(kf) {
			dir, err := filepath.Abs(filepath.Dir(p.path))
			if err != nil {
				dir = filepath.Dir(p.path)
			}
			kf = filepath.Join(dir, kf)
		}
		data, err := os.ReadFile(kf)
		if err != nil {
			warn("not fired: cannot read webhook_key_file " + kf)
			return
		}
		key = strings.TrimRight(strings.SplitN(string(data), "\n", 2)[0], "\r")
	}
	if strings.HasPrefix(key, "Authorization: Bearer ") {
		key = strings.TrimPrefix(key, "Authorization: Bearer ")
	} else {
		key = strings.TrimPrefix(key, "Bearer ")
	}
	body, _ := json.Marshal(struct {
		Target string `json:"target"`
		From   string `json:"from"`
		Verb   string `json:"verb"`
		Inbox  string `json:"inbox"`
		File   string `json:"file"`
	}{target, from, verb, inbox, file})
	delay := 1
	for attempt := 0; ; attempt++ {
		why, retry := postOnce(h.url, key, body, target)
		shown := strings.Replace(why, "\x00", " ", 1)
		if why == "" {
			logEvent("webhook", "result", "ok", "to", target, "from", from, "verb", verb, "attempts", strconv.Itoa(attempt+1), "file", file)
			return
		}
		if !retry || attempt >= h.retries {
			logEvent("webhook", "result", "fail", "to", target, "from", from, "verb", verb, "attempts", strconv.Itoa(attempt+1), "error", shown, "file", file)
			if code, text, ok := strings.Cut(why, "\x00"); ok {
				warn("failed (" + code + ") " + text)
			} else {
				warn("failed (" + why + ")")
			}
			return
		}
		wait := delay
		if wait > h.backoffMax {
			wait = h.backoffMax
		}
		note(fmt.Sprintf("webhook for %s: attempt %d failed (%s); retrying in %ds", target, attempt+1, shown, wait))
		time.Sleep(time.Duration(wait) * time.Second)
		delay *= 2
	}
}

// postOnce returns "" on success, else the reason and whether a retry may help.
func postOnce(url, key string, body []byte, target string) (string, bool) {
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err.Error(), false
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err.Error(), true
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	text := strings.ReplaceAll(string(rb), "\n", " ")
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		say(fmt.Sprintf("WEBHOOK %s %d %s", target, resp.StatusCode, text))
		return "", false
	}
	return fmt.Sprintf("%d\x00%s", resp.StatusCode, text), resp.StatusCode >= 500 || resp.StatusCode == 429
}

func nextSeq(seqPath string) int {
	// Lock-free: each sequence number is a file created with O_EXCL in out-seq.d/.
	// A collision tries the next number; a crash at worst skips one. out-seq.txt is
	// only a hint (last number used) so the search starts near the end.
	dir := strings.TrimSuffix(seqPath, ".txt") + ".d"
	if err := os.MkdirAll(dir, 0o775); err != nil {
		die(err.Error())
	}
	current := 1
	if data, err := os.ReadFile(seqPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Fields(line)
			if len(parts) == 2 && parts[0] == "next" {
				if n, err := strconv.Atoi(parts[1]); err == nil {
					current = n + 1
				}
			}
		}
	}
	for ; ; current++ {
		f, err := os.OpenFile(filepath.Join(dir, strconv.Itoa(current)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o660)
		if err == nil {
			f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) {
			die(err.Error())
		}
	}
	tmp := fmt.Sprintf("%s.tmp.%d", seqPath, os.Getpid())
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("next %d\n", current)), 0o660); err == nil {
		_ = os.Rename(tmp, seqPath)
	}
	return current
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o660)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}

func publishReplace(tmp, dest string) {
	if err := os.Rename(tmp, dest); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			die(tmp + " and " + dest + " are on different filesystems, so the rename would not be atomic")
		}
		die(err.Error())
	}
}

var (
	armPlan     plan
	armMe       string
	armActive   bool
	pingForce   bool
	pingEvery   = envInt("CHANNEL_PING_EVERY", 30)
	pingTimeout = envInt("CHANNEL_PING_TIMEOUT", 300)
)

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v >= 0 {
		return v
	}
	return def
}

// logEvent writes one key=value line to syslog (tag agent-channel) unless
// CHANNEL_SYSLOG=0, and appends it to CHANNEL_LOG_FILE when that is set.
func logEvent(event string, kv ...string) {
	var b strings.Builder
	b.WriteString("event=" + event)
	for i := 0; i+1 < len(kv); i += 2 {
		v := kv[i+1]
		if v == "" || strings.ContainsAny(v, " \t\"=") {
			v = strconv.Quote(v)
		}
		b.WriteString(" " + kv[i] + "=" + v)
	}
	line := b.String()
	if os.Getenv("CHANNEL_SYSLOG") != "0" {
		prio := syslog.LOG_USER | syslog.LOG_INFO
		if strings.Contains(line, "result=fail") || strings.Contains(line, "result=timeout") {
			prio = syslog.LOG_USER | syslog.LOG_WARNING
		}
		if w, err := syslog.New(prio, "agent-channel"); err == nil {
			_ = w.Info(line)
			if prio&0x7 == syslog.LOG_WARNING {
				_ = w.Warning(line)
			}
			w.Close()
		}
	}
	if f := os.Getenv("CHANNEL_LOG_FILE"); f != "" {
		if fh, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o660); err == nil {
			fmt.Fprintf(fh, "%s agent-channel %s\n", time.Now().Format(time.RFC3339), line)
			fh.Close()
		}
	}
}

// readHead returns the VERB and FROM header values and the NONCE body line of a message file.
func readHead(path string) (verb, from, nonce string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	inBody := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "---" && !inBody:
			inBody = true
		case !inBody && strings.HasPrefix(line, "VERB: "):
			verb = strings.TrimSpace(line[6:])
		case !inBody && strings.HasPrefix(line, "FROM: "):
			from = strings.TrimSpace(line[6:])
		case inBody && strings.HasPrefix(line, "NONCE:") && nonce == "":
			nonce = strings.TrimSpace(line[6:])
		}
	}
	return
}

var safeRE = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func pongPath(p plan, me, nonce string) string {
	return filepath.Join(filepath.Dir(p.nodes[me]), "pong-"+safeRE.ReplaceAllString(nonce, "_"))
}

// handleControl answers PING and files PONG for channel-ping, so the agent never sees either.
func handleControl(inbox string, files []string) []string {
	if !armActive {
		return files
	}
	var rest []string
	for _, name := range files {
		full := filepath.Join(inbox, name)
		verb, from, nonce := readHead(full)
		if nonce == "" {
			verb = ""
		}
		switch verb {
		case "PING":
			if hasEdge(armPlan, armMe, from) && nonce != "" {
				quiet = true
				sendMessage(armPlan, armMe, from, "PONG", []byte("NONCE: "+nonce+"\n"), nil)
				quiet = false
			}
			_ = os.Remove(full)
			logEvent("ping-answered", "node", armMe, "from", from, "nonce", nonce)
		case "PONG":
			_ = os.Rename(full, pongPath(armPlan, armMe, nonce))
			logEvent("pong-received", "node", armMe, "from", from, "nonce", nonce)
		default:
			rest = append(rest, name)
		}
	}
	return rest
}

func hasEdge(p plan, src, dst string) bool {
	for _, e := range p.edges {
		if e.src == src && e.dst == dst {
			return true
		}
	}
	return false
}

func cmdPing(path, me string) {
	p := loadPlan(path)
	requireMe(me, p)
	node := filepath.Dir(p.nodes[me])
	requireDir(p.nodes[me], "run channel-setup "+path+" "+me)
	stamp := filepath.Join(node, "ping.stamp")
	if info, err := os.Stat(stamp); err == nil && !pingForce {
		age := time.Since(info.ModTime())
		if age < time.Duration(pingEvery)*time.Minute {
			say(fmt.Sprintf("SKIP %s pinged %ds ago (limit every %d min; --force to override)", me, int(age.Seconds()), pingEvery))
			return
		}
	}
	_ = os.WriteFile(stamp, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o660)
	_ = os.Chtimes(stamp, time.Now(), time.Now())
	type probe struct {
		dest, nonce, file string
		twoWay, done      bool
	}
	var probes []*probe
	start := time.Now()
	for _, e := range p.edges {
		if e.src != me {
			continue
		}
		nonce := fmt.Sprintf("%s-%s-%s-%d", me, e.dst, start.Format("20060102-150405"), os.Getpid())
		pr := &probe{dest: e.dst, nonce: nonce, twoWay: hasEdge(p, e.dst, me)}
		pr.file = sendMessage(p, me, e.dst, "PING", []byte("NONCE: "+nonce+"\n"), nil)
		probes = append(probes, pr)
	}
	deadline := start.Add(time.Duration(pingTimeout) * time.Second)
	left := len(probes)
	for left > 0 && time.Now().Before(deadline) {
		for _, pr := range probes {
			if pr.done {
				continue
			}
			ok := false
			if pr.twoWay {
				pp := pongPath(p, me, pr.nonce)
				if fileExists(pp) {
					_ = os.Remove(pp)
					ok = true
				} else {
					for _, name := range messageNames(p.nodes[me]) {
						full := filepath.Join(p.nodes[me], name)
						if v, _, n := readHead(full); v == "PONG" && n == pr.nonce {
							_ = os.Remove(full)
							ok = true
							break
						}
					}
				}
			} else if !fileExists(pr.file) {
				ok = true
			}
			if ok {
				pr.done = true
				left--
				kind := "PONG"
				if !pr.twoWay {
					kind = "CONSUMED"
				}
				secs := fmt.Sprintf("%.1fs", time.Since(start).Seconds())
				say(kind + " " + pr.dest + " " + secs)
				logEvent("ping", "result", "ok", "from", me, "to", pr.dest, "kind", strings.ToLower(kind), "seconds", secs)
			}
		}
		time.Sleep(poll)
	}
	failed := false
	for _, pr := range probes {
		if pr.done {
			continue
		}
		failed = true
		want := "no PONG"
		if !pr.twoWay {
			want = "PING not consumed"
		}
		say("TIMEOUT " + pr.dest + " (" + want + " after " + strconv.Itoa(pingTimeout) + "s)")
		logEvent("ping", "result", "timeout", "from", me, "to", pr.dest, "nonce", pr.nonce)
	}
	if failed {
		os.Exit(1)
	}
}

func cmdStatus(path string) {
	p := loadPlan(path)
	now := time.Now()
	for _, name := range p.nodeOrder {
		inbox := p.nodes[name]
		entries, err := os.ReadDir(inbox)
		if err != nil {
			why := "missing"
			if errors.Is(err, os.ErrPermission) {
				why = "no permission"
			}
			say("node " + name + " " + inbox + " " + why)
			continue
		}
		files := messageNames(inbox)
		_ = entries
		line := fmt.Sprintf("node %s %s messages %d", name, inbox, len(files))
		if len(files) > 0 {
			if info, err := os.Stat(filepath.Join(inbox, files[0])); err == nil {
				line += fmt.Sprintf(" oldest %ds", int(now.Sub(info.ModTime()).Seconds()))
			}
		}
		claimedDir := filepath.Join(inbox, "claimed")
		var claimed []string
		if ce, err := os.ReadDir(claimedDir); err == nil {
			for _, c := range ce {
				if c.IsDir() || strings.HasSuffix(c.Name(), ".owner") {
					continue
				}
				item := c.Name()
				if info, err := c.Info(); err == nil {
					item += fmt.Sprintf(" %ds", int(now.Sub(info.ModTime()).Seconds()))
				}
				if ob, err := os.ReadFile(filepath.Join(claimedDir, c.Name()+".owner")); err == nil {
					item += " owner=" + strings.TrimSpace(strings.SplitN(string(ob), "\n", 2)[0])
				}
				claimed = append(claimed, item)
			}
		} else if errors.Is(err, os.ErrPermission) {
			line += " claimed no-permission"
		}
		line += fmt.Sprintf(" claimed %d", len(claimed))
		say(line)
		for _, c := range claimed {
			say("  claimed " + c)
		}
	}
}

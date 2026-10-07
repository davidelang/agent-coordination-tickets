// Channel commands. One binary. The name it is invoked as selects the mode.
// channel prints help. channel-setup, channel-arm, and channel-send are links to it.
// --setup, --arm, and --send select the mode when the name is channel.
//
// Standard library only. Build with CGO_ENABLED=0 and -ldflags '-s -w'.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
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
	ops = []string{"<->", ">-<", "<-", "->", "--", "-<", ">-"}
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
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func say(line string) { fmt.Println(line) }

func note(msg string) { fmt.Fprintln(os.Stderr, msg) }

func help() {
	fmt.Print(`channel — one inbox directory per agent

With no mode, this prints help and exits.

  channel --setup FILE NODE [-n|--dry-run] [--whatineedtoknow]
  channel --arm   FILE NODE [--whatineedtoknow]
  channel --send  FILE NODE DEST VERB BODY [REF ...] [--whatineedtoknow]

channel-setup, channel-arm, and channel-send are names for this same program.
The name selects the mode, so those forms do not need --setup, --arm, or --send.

-n, --dry-run       check the plan and create nothing (setup only)
--whatineedtoknow   print the nodes and messages this node uses, then exit
-h, --help          print this help

Options may sit before or after the file and the node. -- ends the options.
`)
}

func main() {
	nameMode := modeFromName(os.Args[0])
	opts, rest, err := gnuGetopt(os.Args[1:], "hn", []string{
		"help", "dry-run", "whatineedtoknow", "setup", "arm", "send",
	})
	if err != nil {
		die(err.Error())
	}
	dry, what, helpFlag := false, false, false
	flagMode := ""
	for _, opt := range opts {
		switch opt {
		case "-h", "--help":
			helpFlag = true
		case "-n", "--dry-run":
			dry = true
		case "--whatineedtoknow":
			what = true
		case "--setup", "--arm", "--send":
			m := strings.TrimPrefix(opt, "--")
			if flagMode != "" && flagMode != m {
				die("pick one of --setup, --arm, or --send")
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
			die("no mode; use --setup, --arm, or --send")
		}
		help()
		return
	}
	invoked := "channel-" + mode
	if nameMode == "" {
		invoked = "channel --" + mode
	}
	switch mode {
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
		if len(rest) < 5 {
			die("usage: " + invoked + " FILE NODE RECIPIENT VERB BODYFILE [REF ...]")
		}
		cmdSend(rest[0], rest[1], rest[2], rest[3], rest[4], rest[5:])
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
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				return nil, nil, fmt.Errorf("option --%s must not have a value", name[:eq])
			}
			if !knownLong[name] {
				return nil, nil, fmt.Errorf("option --%s not recognized", name)
			}
			opts = append(opts, "--"+name)
			continue
		}
		for _, c := range a[1:] {
			if !knownShort[byte(c)] {
				return nil, nil, fmt.Errorf("option -%c not recognized", c)
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
		if parts[0] == "node" && len(parts) == 3 {
			name, inbox := parts[1], parts[2]
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
			die(fmt.Sprintf("line %d: expected 'node NAME PATH', 'verb SRC DST WORD...', or 'message NAME { ... }'", n))
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

func emitWhat(me string, p plan) {
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
	refs := filepath.Join(filepath.Dir(p.path), "refs")
	if err := os.MkdirAll(refs, 0o775); err != nil {
		die(err.Error())
	}
	tryChmod(refs, os.ModeSetgid|0o775)
	inbox := p.nodes[me]
	if err := os.MkdirAll(inbox, 0o775); err != nil {
		die(err.Error())
	}
	node := filepath.Dir(inbox)
	tryChmod(node, os.ModeSetgid|0o750)
	tryChmod(inbox, os.ModeSetgid|os.ModeSticky|0o775)
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
	files := messageNames(inbox)
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
	if oldest == "" {
		return ""
	}
	say("FILE " + filepath.Join(inbox, oldest))
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

func cmdSend(path, me, recipient, verb, bodyPath string, refs []string) {
	p := loadPlan(path)
	requireMe(me, p)
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
	if !containsLine(words, verb) {
		die("verb " + verb + " is not allowed on " + me + " -> " + recipient)
	}
	bodyText, err := os.ReadFile(bodyPath)
	if err != nil {
		die("no body file " + bodyPath)
	}
	checkBody(verb, p.messages[verb].body, string(bodyText))
	for _, ref := range refs {
		if !fileExists(ref) {
			die("no referenced file " + ref)
		}
	}
	requireDir(p.nodes[me], "run channel-setup "+p.path+" "+me)
	inbox := p.nodes[recipient]
	requireDir(inbox, "run channel-setup "+p.path+" "+recipient)
	if found.mode == "wait" {
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
}

func nextSeq(seqPath string) int {
	lockPath := strings.TrimSuffix(seqPath, ".txt") + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o660)
	if err != nil {
		die(err.Error())
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		die(err.Error())
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	current := 1
	if data, err := os.ReadFile(seqPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Fields(line)
			if len(parts) == 2 && parts[0] == "next" {
				n, err := strconv.Atoi(parts[1])
				if err == nil {
					current = n + 1
				}
			}
		}
	}
	if err := os.WriteFile(seqPath+".tmp", []byte(fmt.Sprintf("next %d\n", current)), 0o660); err != nil {
		die(err.Error())
	}
	if err := os.Rename(seqPath+".tmp", seqPath); err != nil {
		die(err.Error())
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

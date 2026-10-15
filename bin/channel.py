#!/usr/bin/env python3
"""Inbox channels. The model chooses a recipient and a verb. This program does the filesystem steps."""

import errno
import fcntl
import getopt
import json
import syslog
import urllib.error
import urllib.request
import os
import re
import shutil
import subprocess
import sys
import time
from datetime import datetime
from pathlib import Path

OPS = ("<->", ">-<", "<-", "->", "--", "-<", ">-")
NAME_RE = re.compile(
    r"^(?P<time>\d{8}-\d{6}\.\d{3})__(?P<sender>.+)__(?P<seq>\d+)\.md$"
)
NODE_RE = re.compile(r"^[A-Za-z][A-Za-z0-9_.-]*$")
# Directory listing interval when inotifywait is absent. The model is not woken during the sleep.
POLL_SECONDS = 0.2


class PlanError(Exception):
    pass


def die(msg):
    print(msg, file=sys.stderr)
    raise SystemExit(1)


def read_text(path):
    return Path(path).read_text()


def strip_comments(text):
    text = re.sub(r"/\*.*?\*/", "", text, flags=re.S)
    text = re.sub(r"//.*?$", "", text, flags=re.M)
    text = re.sub(r"#.*?$", "", text, flags=re.M)
    return text


def parse_diag(text):
    """Return a list of (src, op, dst, label)."""
    body = strip_comments(text)
    edges = []
    op_re = re.compile("|".join(re.escape(op) for op in OPS))
    for stmt in body.split(";"):
        label = ""
        attr = re.search(r"\[(.*)\]", stmt, flags=re.S)
        if attr:
            found = re.search(
                r"label\s*=\s*(?:\"([^\"]*)\"|'([^']*)'|([A-Za-z0-9_.-]+))",
                attr.group(1),
            )
            if found:
                label = next(group for group in found.groups() if group is not None)
            stmt = stmt[: attr.start()] + stmt[attr.end() :]
        ops = op_re.findall(stmt)
        if not ops:
            if re.search(r"[<>]", stmt):
                die(f"unrecognized arrow in: {stmt.strip()}. Use ->, <->, or -<.")
            continue
        if re.search(r"[<>]", op_re.sub(" ", stmt)):
            die(f"unrecognized arrow in: {stmt.strip()}. Use ->, <->, or -<.")
        nodes = []
        for index, part in enumerate(op_re.split(stmt)):
            names = re.findall(r"[A-Za-z_][A-Za-z0-9_.-]*", part)
            names = [name for name in names if name not in {"blockdiag", "group"}]
            if not names:
                continue
            nodes.append(names[-1] if index == 0 else names[0])
        if len(nodes) != len(ops) + 1:
            die(f"could not read an edge statement: {stmt.strip()}")
        for index, op in enumerate(ops):
            edges.append((nodes[index], op, nodes[index + 1], label))
    return edges


def expand(edges):
    """Directed (src, dst, mode, label). mode is wait or queue."""
    out = []
    for src, op, dst, label in edges:
        if op == "<->":
            out.append((src, dst, "wait", label))
            out.append((dst, src, "wait", label))
        elif op == "->":
            out.append((src, dst, "wait", label))
        elif op == "-<":
            out.append((src, dst, "queue", label))
        elif op in {"<-", "--", ">-", ">-<"}:
            die(f"{op} is drawn by blockdiag and is not a channel. Use ->, <->, or -<. See docs/diagram-marks.md.")
        else:
            die(f"unknown operator {op}")
    return out


def absolute_path(lineno, label, value):
    path = Path(value)
    if not path.is_absolute():
        die(f"line {lineno}: {label} path must be absolute")
    return path


def after_diagram(text):
    """Text after the diagram's closing brace.

    Brace counting skips comments so a brace in a note is not the diagram.
    The returned text still contains the block comment. The key parser reads
    the lines inside it.
    """
    i = 0
    n = len(text)
    depth = 0
    started = False
    while i < n:
        if text.startswith("/*", i):
            end = text.find("*/", i + 2)
            if end < 0:
                die("unclosed block comment in plan file")
            i = end + 2
            continue
        if text[i] == "#" or text.startswith("//", i):
            nl = text.find("\n", i)
            i = n if nl < 0 else nl + 1
            continue
        if text[i] in "\"'":
            quote = text[i]
            i += 1
            while i < n and text[i] != quote:
                if text[i] == "\\":
                    i += 2
                    continue
                i += 1
            i += 1
            continue
        if text[i] == "{":
            depth += 1
            started = True
        elif text[i] == "}":
            depth -= 1
            if started and depth == 0:
                return text[i + 1 :]
        i += 1
    die("plan file has no closed blockdiag diagram")


def blank_span(chars, start, end):
    for index in range(start, end):
        if chars[index] != "\n":
            chars[index] = " "


def matching_brace(text, open_at):
    depth = 0
    for index in range(open_at, len(text)):
        if text[index] == "{":
            depth += 1
        elif text[index] == "}":
            depth -= 1
            if depth == 0:
                return index
    return None


def split_message(name, interior):
    found = re.search(r"body[ \t]*:[ \t]*\{", interior)
    if found is None:
        die(f"message {name} has no body")
    open_at = found.end() - 1
    close_at = matching_brace(interior, open_at)
    if close_at is None:
        die(f"message {name} body is missing its closing brace")
    body = [line.strip() for line in interior[open_at + 1 : close_at].splitlines() if line.strip()]
    if not body:
        die(f"message {name} has an empty body")
    desc = (interior[: found.start()] + interior[close_at + 1 :]).strip()
    return {"desc": desc, "body": body}


def extract_messages(text):
    """Pull message blocks out of the key. Comments outside them stay put."""
    messages = {}
    chars = list(text)
    cursor = 0
    pattern = re.compile(r"(?m)^[ \t]*message[ \t]+([A-Za-z][A-Za-z0-9_.-]*)[ \t]*\{")
    while True:
        found = pattern.search(text, cursor)
        if found is None:
            break
        name = found.group(1)
        open_at = found.end() - 1
        close_at = matching_brace(text, open_at)
        if close_at is None:
            die(f"message {name} is missing its closing brace")
        if name in messages:
            die(f"message {name} is defined twice")
        messages[name] = split_message(name, text[open_at + 1 : close_at])
        blank_span(chars, found.start(), close_at + 1)
        cursor = close_at + 1
    return messages, "".join(chars)


HOOKS = {}
OPTS = {"msgs": [], "force": False,
        "every": int(os.environ.get("CHANNEL_PING_EVERY", "30") or 30),
        "timeout": int(os.environ.get("CHANNEL_PING_TIMEOUT", "300") or 300)}
ARM = {"me": None, "plan": None}


def log_event(event, **kv):
    """One key=value line to syslog (tag agent-channel) unless CHANNEL_SYSLOG=0, and to CHANNEL_LOG_FILE."""
    parts = [f"event={event}"]
    for key, val in kv.items():
        val = str(val)
        if val == "" or any(c in val for c in ' \t"='):
            val = '"' + val.replace("\\", "\\\\").replace('"', '\\"') + '"'
        parts.append(f"{key}={val}")
    line = " ".join(parts)
    if os.environ.get("CHANNEL_SYSLOG", "1") != "0":
        prio = syslog.LOG_WARNING if ("result=fail" in line or "result=timeout" in line) else syslog.LOG_INFO
        try:
            syslog.openlog("agent-channel", 0, syslog.LOG_USER)
            syslog.syslog(prio, line)
        except OSError:
            pass
    log_file = os.environ.get("CHANNEL_LOG_FILE")
    if log_file:
        try:
            with open(log_file, "a") as handle:
                handle.write(datetime.now().astimezone().isoformat(timespec="seconds") + " agent-channel " + line + "\n")
        except OSError:
            pass


def parse_key(text, line_base=0):
    messages, blanked = extract_messages(text)
    nodes = {}
    verbs = {}  # (src, dst) -> [words]
    for lineno, raw in enumerate(blanked.splitlines(), line_base + 1):
        line = raw.strip()
        if not line or line in {"/*", "*/"} or line.startswith("#") or line.startswith("//"):
            continue
        parts = line.split()
        if parts[0] == "node" and len(parts) >= 3:
            name, inbox = parts[1], parts[2]
            hook = {"url": "", "key_file": "", "key": "", "retries": 3, "backoff_max": 30}
            for kv in parts[3:]:
                key, eq, val = kv.partition("=")
                if not eq or not key:
                    die(f"line {lineno}: node option must be key=value ({kv})")
                if not val:
                    die(f"line {lineno}: {key} is empty")
                if key == "webhook":
                    if not re.match(r"https?://", val):
                        die(f"line {lineno}: webhook must start with http:// or https://")
                    hook["url"] = val
                elif key == "webhook_key_file":
                    hook["key_file"] = val
                elif key == "webhook_key":
                    hook["key"] = val
                elif key in ("webhook_retries", "webhook_backoff_max"):
                    if not val.isdigit():
                        die(f"line {lineno}: {key} must be a whole number >= 0")
                    hook["retries" if key == "webhook_retries" else "backoff_max"] = int(val)
                else:
                    die(f"line {lineno}: unknown node option {key} (allowed: webhook, webhook_key_file, webhook_key, webhook_retries, webhook_backoff_max)")
            if hook["key_file"] and hook["key"]:
                die(f"line {lineno}: use webhook_key_file or webhook_key, not both")
            if not hook["url"] and (hook["key_file"] or hook["key"]):
                die(f"line {lineno}: webhook key given without webhook=URL")
            if hook["url"]:
                HOOKS[name] = hook
            if not valid_node(name):
                die(f"line {lineno}: node name must be letters, digits, dot, underscore, or hyphen, and not contain __")
            if name in nodes:
                die(f"line {lineno}: node {name} is listed twice")
            nodes[name] = absolute_path(lineno, "inbox", inbox)
        elif parts[0] == "verb" and len(parts) >= 4:
            src, dst, words = parts[1], parts[2], parts[3:]
            if (src, dst) in verbs:
                die(f"line {lineno}: verb {src} {dst} is listed twice")
            verbs[(src, dst)] = words
        else:
            die(f"line {lineno}: expected 'node NAME PATH [webhook=URL [webhook_key_file=PATH|webhook_key=TOKEN]]', 'verb SRC DST WORD...', or 'message NAME {{ ... }}'")
    return nodes, verbs, messages


def load_plan(plan_path):
    plan_path = Path(plan_path)
    if not plan_path.is_file():
        die(f"no plan file {plan_path}")
    text = read_text(plan_path)
    edges = expand(parse_diag(text))
    tail = after_diagram(text)
    base = text[: len(text) - len(tail)].count("\n")
    nodes, verbs, messages = parse_key(tail, base)
    problems = []
    seen = set()
    for src, dst, _mode, _label in edges:
        if src not in nodes or dst not in nodes:
            problems.append(f"{src} -> {dst} uses a node the plan file does not define")
        if (src, dst) not in verbs:
            problems.append(f"{src} -> {dst} has no verb line")
        if (src, dst) in seen:
            problems.append(f"duplicate edge {src} -> {dst}")
        seen.add((src, dst))
    for name in nodes:
        if name not in {node for pair in seen for node in pair}:
            problems.append(f"node {name} is in the key and not in the diagram")
    for (src, dst), words in verbs.items():
        if (src, dst) not in seen:
            problems.append(f"verb {src} {dst} is not an arrow in the diagram")
        for word in words:
            if word not in messages:
                problems.append(f"{word} on {src} -> {dst} has no message definition")
    if problems:
        die("\n".join(problems))
    return plan_path, nodes, verbs, messages, edges


def valid_node(name):
    return bool(NODE_RE.match(name)) and "__" not in name and name not in {"blockdiag", "group"}


def require_me(me, nodes, plan_path):
    if not valid_node(me):
        die(f"node must be a bare name, not a path ({me})")
    if me not in nodes:
        die(f"{me} is not a node in {plan_path}")
    return me


def node_dir(inbox):
    return inbox.parent


def out_seq_path(inbox):
    return node_dir(inbox) / "out-seq.txt"


def read_map(path):
    found = {}
    if path.is_file():
        for raw in path.read_text().splitlines():
            parts = raw.split()
            if len(parts) == 2 and parts[1].isdigit():
                found[parts[0]] = int(parts[1])
    return found


def write_map(path, found):
    text = "".join(f"{key} {found[key]}\n" for key in sorted(found))
    tmp = path.with_suffix(".tmp")
    tmp.write_text(text)
    os.replace(tmp, path)


def next_seq(seq_path):
    """Lock-free: create out-seq.d/N with O_EXCL; on collision try N+1. out-seq.txt is a hint."""
    seq_dir = seq_path.with_suffix(".d")
    seq_dir.mkdir(parents=True, exist_ok=True)
    current = read_map(seq_path).get("next", 0) + 1
    while True:
        try:
            os.close(os.open(seq_dir / str(current), os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o660))
            break
        except FileExistsError:
            current += 1
    try:
        tmp = seq_path.with_name(f"{seq_path.name}.tmp.{os.getpid()}")
        tmp.write_text(f"next {current}\n")
        os.replace(tmp, seq_path)
    except OSError:
        pass
    return current


def lock_exclusive(path):
    path.parent.mkdir(parents=True, exist_ok=True)
    handle = open(path, "a+")
    fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
    return handle


def parse_message_name(name):
    match = NAME_RE.match(name)
    if not match:
        return None
    return match.group("sender"), int(match.group("seq"))


def message_names(directory):
    if not directory.is_dir():
        return []
    found = []
    for entry in directory.iterdir():
        if entry.is_file() and parse_message_name(entry.name):
            found.append(entry)
    return sorted(found, key=lambda item: item.name)


def edge_between(edges, src, dst):
    for item in edges:
        if item[0] == src and item[1] == dst:
            return item
    return None


def try_chmod(path, mode):
    try:
        os.chmod(path, mode)
    except OSError as exc:
        print(f"chmod {oct(mode)} {path} failed: {exc}", file=sys.stderr)


def say(line):
    print(line, flush=True)


def split_args(argv):
    """Return (dry_run, what, positionals).

    gnu_getopt is the getopt_long scan: short and long options may sit
    before, among, or after the file and node. A lone -- ends the options.
    """
    try:
        found, rest = getopt.gnu_getopt(argv, "nm:", ["dry-run", "whatineedtoknow", "force", "every=", "timeout="])
    except getopt.GetoptError as exc:
        die(str(exc))
    dry_run = False
    what = False
    for opt, _value in found:
        if opt in {"-n", "--dry-run"}:
            dry_run = True
        elif opt == "--whatineedtoknow":
            what = True
        elif opt == "-m":
            OPTS["msgs"].append(_value)
        elif opt == "--force":
            OPTS["force"] = True
        elif opt in ("--every", "--timeout"):
            if not _value.isdigit():
                die(f"{opt} needs a whole number")
            OPTS[opt[2:]] = int(_value)
    return dry_run, what, rest


def report_edges(plan_path, edges):
    say(f"OK {plan_path.resolve()}")
    for src, dst, mode, label in edges:
        extra = f" {label}" if label else ""
        say(f"{src} -> {dst} {mode}{extra}")


ACT_LINES = [
    'how to act:',
    'you are node {NODE}',
    'plan: {PLAN}',
    'inbox: {INBOX}',
    'watch: {BIN}/channel-arm {PLAN} {NODE}',
    '  Leave the watch running as a persistent monitor. It prints READY, then FILE <path> for the oldest message in your inbox, and the next oldest only after that file is removed.',
    '  The watch answers PING for you (it sends PONG and removes the PING) and never shows it. With no watch running, answer a PING yourself: send PONG to its sender with the same NONCE line, then rm the PING.',
    'start: before new messages, look in {INBOX}/claimed/. Those are messages you already started and may have partly done. Check whether your reply was already sent before redoing anything. Nothing reclaims a claim by age; report claims you cannot finish.',
    'read: a message file has FROM, TO, SEQ, VERB and REFS (zero or more files, paths relative to the plan directory, or - none), then a --- line, then the body.',
    '  Handle the oldest FILE first, one file at a time. You may move it into {INBOX}/claimed/ while you work (the watch ignores subfolders and shows the next file); optionally write claimed/<file>.owner saying who has it.',
    'send: {BIN}/channel-send {PLAN} {NODE} DEST VERB BODYFILE [REF ...]',
    "  or:  {BIN}/channel-send {PLAN} {NODE} DEST VERB -m 'TEXT' [-m 'TEXT' ...] [REF ...]   (each -m is one block of lines; blocks are joined by a blank line)",
    '  or:  {BIN}/channel-send {PLAN} {NODE} DEST VERB - [REF ...]   (- or no BODYFILE reads the body from stdin)',
    '  DEST must be a node you have an arrow to and VERB must be listed on that arrow (see the verb lines below). PING and PONG are built in on every arrow; their body is NONCE: <value>.',
    '  Body: copy every line without <...> exactly. For a line with <...>, keep the text before < and replace <...> with a real value.',
    '  Lines are compared after trimming spaces; blank lines are ignored and extra lines are allowed.',
    '  REF files are optional and a message may carry several; each is copied into refs/ and listed under REFS.',
    '  On a -> arrow the send waits until DEST removes your previous message; on -< it queues. PING and PONG never wait.',
    'done: rm the FILE when you have finished it (from claimed/ only after your reply is sent). Removing it is the acknowledgement.',
    'tools: {BIN}/channel-ping {PLAN} {NODE} tests each of your arrows (skipped if run in the last 30 minutes; --force, --every MIN).',
    "  {BIN}/channel-status {PLAN} shows each inbox's message count, oldest age, and claimed files.",
    'after a start or a compact, run {BIN}/channel-arm {PLAN} {NODE} --whatineedtoknow again, then check claimed/ (see start).',
    '',
]


def emit_act(me, plan_path, nodes):
    bin_dir = os.path.dirname(os.path.abspath(sys.argv[0]))
    plan = os.path.abspath(str(plan_path))
    inbox = os.path.abspath(str(nodes[me]))
    for line in ACT_LINES:
        say(line.replace("{NODE}", me).replace("{PLAN}", plan).replace("{INBOX}", inbox).replace("{BIN}", bin_dir))


def emit_what(me, nodes, verbs, messages, edges, plan_path):
    emit_act(me, plan_path, nodes)
    """Nodes this one talks to, and the message blocks on those arrows."""
    related = {me}
    touching = []
    for src, dst, mode, _label in edges:
        if src != me and dst != me:
            continue
        related.add(src)
        related.add(dst)
        touching.append((src, dst, mode))
    say(f"node {me} {nodes[me]}")
    for name in sorted(n for n in related if n != me):
        say(f"node {name} {nodes[name]}")
    used = []
    for src, dst, mode in touching:
        say(f"{src} -> {dst} {mode}")
        words = verbs[(src, dst)]
        say("verb " + " ".join([src, dst, *words]))
        used.extend(words)
    seen = set()
    for word in used:
        if word in seen:
            continue
        seen.add(word)
        msg = messages[word]
        say(f"message {word} {{")
        if msg["desc"]:
            say(msg["desc"])
        say("body: {")
        for line in msg["body"]:
            say(line)
        say("}")
        say("}")


def show_what(plan_path, me):
    plan_path, nodes, verbs, messages, edges = load_plan(plan_path)
    require_me(me, nodes, plan_path)
    emit_what(me, nodes, verbs, messages, edges, plan_path)


def require_dir(path, hint):
    if not path.is_dir():
        die(f"no directory {path}; {hint}")


def cmd_setup(plan_path, me, dry_run, what):
    plan_path, nodes, verbs, messages, edges = load_plan(plan_path)
    require_me(me, nodes, plan_path)
    if dry_run:
        report_edges(plan_path, edges)
        say("dry-run")
    if what:
        emit_what(me, nodes, verbs, messages, edges, plan_path)
    if dry_run or what:
        return
    old = os.umask(0o002)
    try:
        # Rerunning on an existing node is safe: create whatever is missing, change nothing that exists.
        refs = plan_path.parent / "refs"
        if not refs.is_dir():
            refs.mkdir(parents=True, exist_ok=True)
            try_chmod(refs, 0o2775)
        inbox = nodes[me]
        if inbox.is_dir():
            say(f"node {me} inbox {inbox} existing")
            return
        inbox.mkdir(parents=True, exist_ok=True)
        node = node_dir(inbox)
        try_chmod(node, 0o2775)
        try_chmod(inbox, 0o2775)
        say(f"node {me} inbox {inbox}")
    finally:
        os.umask(old)


def note(msg):
    print(msg, file=sys.stderr, flush=True)


def use_poll():
    return os.environ.get("CHANNEL_POLL") == "1"


def try_inotify(args):
    """Start inotifywait, or return None so the caller lists the directory."""
    if use_poll() or shutil.which("inotifywait") is None:
        return None
    try:
        proc = subprocess.Popen(
            args,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
        )
    except OSError:
        return None
    if proc.stdout is None:
        proc.terminate()
        proc.wait()
        return None
    time.sleep(0.05)
    if proc.poll() is not None:
        proc.wait()
        return None
    return proc


def stop_proc(proc):
    if proc is None:
        return
    if proc.poll() is None:
        proc.terminate()
    proc.wait()


def announce(inbox, shown):
    """Print the oldest inbox file when it is not the one already announced."""
    if not inbox.is_dir():
        die(f"inbox disappeared: {inbox}")
    handle_control(inbox)
    files = message_names(inbox)
    oldest = files[0].name if files else None
    if shown and shown != oldest and not (inbox / shown).is_file():
        log_event("consumed", node=ARM["me"], file=inbox / shown)
    if oldest == shown:
        return shown
    if shown and (inbox / shown).is_file() and oldest == shown:
        return shown
    if oldest is None:
        return None
    if shown and (inbox / shown).is_file():
        return shown
    say(f"FILE {inbox / oldest}")
    log_event("deliver", node=ARM["me"], file=inbox / oldest)
    return oldest


def watch_oldest(inbox, shown):
    while True:
        shown = announce(inbox, shown)
        time.sleep(POLL_SECONDS)


def cmd_arm(plan_path, me):
    loaded = load_plan(plan_path)
    plan_path, nodes, _verbs, _messages, _edges = loaded
    require_me(me, nodes, plan_path)
    ARM["me"], ARM["plan"] = me, loaded
    inbox = nodes[me]
    require_dir(inbox, f"run channel-setup {plan_path} {me}")
    proc = try_inotify(
        [
            "inotifywait",
            "-m",
            "-q",
            "-e",
            "moved_to",
            "-e",
            "moved_from",
            "-e",
            "delete",
            "--format",
            "%f",
            str(inbox),
        ]
    )
    if proc is None:
        note(
            "channel-arm is listing the inbox every "
            f"{POLL_SECONDS}s. inotifywait is not in use."
        )
    say("READY")
    shown = announce(inbox, None)
    if proc is None:
        watch_oldest(inbox, shown)
        return
    try:
        for _line in proc.stdout:
            shown = announce(inbox, shown)
    finally:
        stop_proc(proc)
    note("inotifywait ended; listing the inbox from here on.")
    watch_oldest(inbox, shown)


def wait_until_gone(path):
    while path.exists():
        time.sleep(POLL_SECONDS)


def wait_until_removed(inbox, name):
    say(f"PENDING {inbox / name}")
    target = inbox / name
    proc = try_inotify(
        [
            "inotifywait",
            "-m",
            "-q",
            "-e",
            "moved_from",
            "-e",
            "delete",
            "--format",
            "%f",
            str(inbox),
        ]
    )
    if proc is None:
        note(
            "channel-send is listing the inbox every "
            f"{POLL_SECONDS}s until the pending file is removed. inotifywait is not in use."
        )
        wait_until_gone(target)
        return
    try:
        if not target.exists():
            return
        for line in proc.stdout:
            if not target.exists():
                return
    finally:
        stop_proc(proc)
    if target.exists():
        note("inotifywait ended; listing the inbox until the pending file is removed.")
        wait_until_gone(target)


def publish_replace(tmp, dest):
    try:
        os.replace(tmp, dest)
    except OSError as exc:
        if exc.errno == errno.EXDEV:
            die(f"{tmp} and {dest} are on different filesystems, so the rename would not be atomic")
        raise


def check_body(verb, template, text):
    sent = [line.strip() for line in text.splitlines() if line.strip()]
    for raw in template:
        if "<" in raw and raw.find("<") < raw.rfind(">"):
            prefix = raw.split("<", 1)[0]
            filled = False
            for line in sent:
                if prefix and not line.startswith(prefix):
                    continue
                value = line[len(prefix) :].strip() if prefix else line
                if value and not (value.startswith("<") and value.endswith(">")):
                    filled = True
                    break
            if not filled:
                die(f"{verb} body is missing a value for: {raw}")
        elif raw not in sent:
            die(f"{verb} body is missing the line: {raw}")


CONTROL = ("PING", "PONG")


def cmd_send(plan_path, me, recipient, verb, body_text, refs):
    loaded = load_plan(plan_path)
    require_me(me, loaded[1], loaded[0])
    return send_message(loaded, me, recipient, verb, body_text, refs)


def send_message(loaded, me, recipient, verb, body_text, refs):
    plan_file, nodes, verbs, messages, edges = loaded
    if recipient not in nodes:
        die(f"unknown node (recipient={recipient})")
    found = edge_between(edges, me, recipient)
    if found is None:
        die(f"{me} is not allowed to send to {recipient}")
    _src, _dst, mode, _label = found
    allowed = verbs.get((me, recipient), [])
    if verb not in allowed and verb not in CONTROL:
        die(f"verb {verb} is not allowed on {me} -> {recipient}")
    template = messages[verb]["body"] if verb in messages else ["NONCE: <nonce>"]
    check_body(verb, template, body_text)
    published = []
    for ref in refs:
        source = Path(ref)
        if not source.is_file():
            die(f"no referenced file {source}")
        published.append(source)
    require_dir(nodes[me], f"run channel-setup {plan_file} {me}")
    inbox = nodes[recipient]
    require_dir(inbox, f"run channel-setup {plan_file} {recipient}")
    control = verb in CONTROL and re.search(r"^[ \t]*NONCE:[ \t]*\S", body_text, re.M)
    if mode == "wait" and not control:
        pending = [entry for entry in message_names(inbox) if parse_message_name(entry.name)[0] == me]
        for entry in pending:
            wait_until_removed(inbox, entry.name)
    for source in published:
        if not source.is_file():
            die(f"no referenced file {source}")
    seq_path = out_seq_path(nodes[me])
    current = next_seq(seq_path)
    now = datetime.now()
    stamp = now.strftime("%Y%m%d-%H%M%S") + f".{now.microsecond // 1000:03d}"
    refs_dir = plan_file.parent / "refs"
    require_dir(refs_dir, f"run channel-setup {plan_file} {me}")
    copied = []
    for source in published:
        dest = refs_dir / f"{stamp}__{me}__{current:04d}__{source.name}"
        shutil.copyfile(source, dest)
        try_chmod(dest, 0o660)
        copied.append(dest.relative_to(plan_file.parent).as_posix())
    header = [
        f"FROM: {me}",
        f"TO: {recipient}",
        f"SEQ: {current}",
        f"VERB: {verb}",
        "REFS:",
    ]
    header.extend(f"- {item}" for item in copied)
    if not copied:
        header.append("- none")
    header.append("---")
    text = "\n".join(header) + "\n" + body_text
    if not text.endswith("\n"):
        text += "\n"
    filename = f"{stamp}__{me}__{current:04d}.md"
    tmp = node_dir(nodes[me]) / f".tmp-{filename}"
    tmp.write_text(text)
    try_chmod(tmp, 0o660)
    dest = inbox / filename
    publish_replace(tmp, dest)
    say(f"SENT {dest}")
    log_event("send", plan=plan_file, **{"from": me}, to=recipient, verb=verb, seq=current, refs=len(copied), file=dest)
    fire_webhook(plan_file, recipient, me, verb, inbox, dest)
    return dest


def fire_webhook(plan_file, target, sender, verb, inbox, file):
    hook = HOOKS.get(target)
    if not hook:
        return
    key = hook["key"]
    if hook["key_file"]:
        key_file = Path(hook["key_file"])
        if not key_file.is_absolute():
            key_file = plan_file.resolve().parent / key_file
        try:
            key = key_file.read_text().split("\n", 1)[0].rstrip("\r")
        except OSError:
            note(f"WARNING: webhook for {target} not fired: cannot read webhook_key_file {key_file} (message is queued)")
            return
    for prefix in ("Authorization: Bearer ", "Bearer "):
        if key.startswith(prefix):
            key = key[len(prefix):]
            break
    payload = json.dumps({"target": target, "from": sender, "verb": verb, "inbox": str(inbox), "file": str(file)}, separators=(",", ":")).encode()
    delay = 1
    attempt = 0
    while True:
        attempt += 1
        req = urllib.request.Request(hook["url"], data=payload, method="POST", headers={"Content-Type": "application/json"})
        if key:
            req.add_header("Authorization", "Bearer " + key)
        why, retry = "", False
        try:
            with urllib.request.urlopen(req, timeout=15) as resp:
                text = resp.read(300).decode(errors="replace").replace("\n", " ")
                say(f"WEBHOOK {target} {resp.status} {text}")
                log_event("webhook", result="ok", to=target, **{"from": sender}, verb=verb, attempts=attempt, file=file)
                return
        except urllib.error.HTTPError as exc:
            why, retry = f"{exc.code} {exc.read(300).decode(errors='replace')}", exc.code >= 500 or exc.code == 429
        except (urllib.error.URLError, OSError) as exc:
            why, retry = str(exc), True
        if not retry or attempt > hook["retries"]:
            log_event("webhook", result="fail", to=target, **{"from": sender}, verb=verb, attempts=attempt, error=why, file=file)
            note(f"WARNING: webhook for {target} failed ({why}) (message is queued)")
            return
        wait = min(delay, hook["backoff_max"])
        note(f"webhook for {target}: attempt {attempt} failed ({why}); retrying in {wait}s")
        time.sleep(wait)
        delay *= 2


def read_head(path):
    verb = sender = nonce = ""
    try:
        lines = Path(path).read_text().splitlines()
    except OSError:
        return verb, sender, nonce
    in_body = False
    for line in lines:
        line = line.strip()
        if line == "---" and not in_body:
            in_body = True
        elif not in_body and line.startswith("VERB: "):
            verb = line[6:].strip()
        elif not in_body and line.startswith("FROM: "):
            sender = line[6:].strip()
        elif in_body and line.startswith("NONCE:") and not nonce:
            nonce = line[6:].strip()
    return verb, sender, nonce


def pong_path(nodes, me, nonce):
    return node_dir(nodes[me]) / ("pong-" + re.sub(r"[^A-Za-z0-9_.-]", "_", nonce))


def handle_control(inbox):
    """channel-arm answers PING and files PONG for channel-ping; the agent never sees either."""
    me, loaded = ARM["me"], ARM["plan"]
    if not me:
        return
    for entry in message_names(inbox):
        verb, sender, nonce = read_head(entry)
        if not nonce:
            continue
        if verb == "PING":
            if nonce and edge_between(loaded[4], me, sender):
                import contextlib, io
                with contextlib.redirect_stdout(io.StringIO()):
                    send_message(loaded, me, sender, "PONG", f"NONCE: {nonce}\n", [])
            entry.unlink(missing_ok=True)
            log_event("ping-answered", node=me, **{"from": sender}, nonce=nonce)
        elif verb == "PONG":
            try:
                os.replace(entry, pong_path(loaded[1], me, nonce))
            except OSError:
                pass
            log_event("pong-received", node=me, **{"from": sender}, nonce=nonce)


def cmd_ping(plan_path, me):
    loaded = load_plan(plan_path)
    plan_file, nodes, _verbs, _messages, edges = loaded
    require_me(me, nodes, plan_file)
    require_dir(nodes[me], f"run channel-setup {plan_file} {me}")
    stamp = node_dir(nodes[me]) / "ping.stamp"
    if stamp.is_file() and not OPTS["force"]:
        age = time.time() - stamp.stat().st_mtime
        if age < OPTS["every"] * 60:
            say(f"SKIP {me} pinged {int(age)}s ago (limit every {OPTS['every']} min; --force to override)")
            return
    stamp.write_text(datetime.now().astimezone().isoformat(timespec="seconds") + "\n")
    start = time.time()
    probes = []
    for src, dst, _mode, _label in edges:
        if src != me:
            continue
        nonce = f"{me}-{dst}-{datetime.now().strftime('%Y%m%d-%H%M%S')}-{os.getpid()}"
        sent = send_message(loaded, me, dst, "PING", f"NONCE: {nonce}\n", [])
        probes.append({"dest": dst, "nonce": nonce, "file": sent, "two": edge_between(edges, dst, me) is not None, "done": False})
    deadline = start + OPTS["timeout"]
    while any(not pr["done"] for pr in probes) and time.time() < deadline:
        for pr in probes:
            if pr["done"]:
                continue
            ok = False
            if pr["two"]:
                pp = pong_path(nodes, me, pr["nonce"])
                if pp.is_file():
                    pp.unlink()
                    ok = True
                else:
                    for entry in message_names(nodes[me]):
                        verb, _sender, nonce = read_head(entry)
                        if verb == "PONG" and nonce == pr["nonce"]:
                            entry.unlink(missing_ok=True)
                            ok = True
                            break
            elif not Path(pr["file"]).is_file():
                ok = True
            if ok:
                pr["done"] = True
                kind = "PONG" if pr["two"] else "CONSUMED"
                secs = f"{time.time() - start:.1f}s"
                say(f"{kind} {pr['dest']} {secs}")
                log_event("ping", result="ok", **{"from": me}, to=pr["dest"], kind=kind.lower(), seconds=secs)
        time.sleep(POLL_SECONDS)
    failed = False
    for pr in probes:
        if pr["done"]:
            continue
        failed = True
        want = "no PONG" if pr["two"] else "PING not consumed"
        say(f"TIMEOUT {pr['dest']} ({want} after {OPTS['timeout']}s)")
        log_event("ping", result="timeout", **{"from": me}, to=pr["dest"], nonce=pr["nonce"])
    if failed:
        sys.exit(1)


def cmd_status(plan_path):
    _plan_file, nodes, _verbs, _messages, _edges = load_plan(plan_path)
    now = time.time()
    for name, inbox in nodes.items():
        try:
            os.listdir(inbox)
        except PermissionError:
            say(f"node {name} {inbox} no permission")
            continue
        except OSError:
            say(f"node {name} {inbox} missing")
            continue
        files = message_names(inbox)
        line = f"node {name} {inbox} messages {len(files)}"
        if files:
            line += f" oldest {int(now - files[0].stat().st_mtime)}s"
        claimed = []
        cdir = inbox / "claimed"
        try:
            for entry in sorted(cdir.iterdir()) if cdir.is_dir() else []:
                if entry.is_dir() or entry.name.endswith(".owner"):
                    continue
                item = f"{entry.name} {int(now - entry.stat().st_mtime)}s"
                owner = cdir / (entry.name + ".owner")
                if owner.is_file():
                    item += " owner=" + owner.read_text().split("\n", 1)[0].strip()
                claimed.append(item)
        except PermissionError:
            line += " claimed no-permission"
        say(f"{line} claimed {len(claimed)}")
        for item in claimed:
            say(f"  claimed {item}")


def main(argv):
    try:
        sys.stdout.reconfigure(line_buffering=True)
    except (AttributeError, OSError):
        pass
    if len(argv) < 2:
        die("usage: channel.py setup|arm|send ...")
    command = argv[1]
    dry_run, what, args = split_args(argv[2:])
    if OPTS["msgs"] and command != "send":
        die("-m is only for channel-send")
    if command == "setup":
        if len(args) != 2:
            die("usage: channel-setup FILE NODE [-n|--dry-run] [--whatineedtoknow]")
        cmd_setup(args[0], args[1], dry_run, what)
    elif command == "arm":
        if dry_run:
            die("--dry-run is only for channel-setup")
        if what:
            if len(args) != 2:
                die("usage: channel-arm FILE NODE --whatineedtoknow")
            show_what(args[0], args[1])
            return
        if len(args) != 2:
            die("usage: channel-arm FILE NODE")
        cmd_arm(args[0], args[1])
    elif command == "send":
        if dry_run:
            die("--dry-run is only for channel-setup")
        if what:
            if len(args) != 2:
                die("usage: channel-send FILE NODE --whatineedtoknow")
            show_what(args[0], args[1])
            return
        if len(args) < 4:
            die("usage: channel-send FILE NODE RECIPIENT VERB [BODYFILE|-|-m TEXT...] [REF ...]")
        if OPTS["msgs"]:
            body_text, refs = "\n\n".join(OPTS["msgs"]) + "\n", args[4:]
        elif len(args) == 4 or args[4] == "-":
            body_text, refs = sys.stdin.read(), args[5:]
        else:
            body = Path(args[4])
            if not body.is_file():
                die(f"no body file {body}")
            body_text, refs = body.read_text(), args[5:]
        cmd_send(args[0], args[1], args[2], args[3], body_text, refs)
    elif command == "ping":
        if dry_run or what or len(args) != 2:
            die("usage: channel-ping FILE NODE [--force] [--every MIN] [--timeout SEC]")
        cmd_ping(args[0], args[1])
    elif command == "status":
        if dry_run or what or len(args) != 1:
            die("usage: channel-status FILE")
        cmd_status(args[0])
    else:
        die(f"unknown command {command}")


if __name__ == "__main__":
    try:
        main(sys.argv)
    except PlanError as exc:
        die(str(exc))

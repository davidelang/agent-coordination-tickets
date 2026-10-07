#!/usr/bin/env python3
"""Inbox channels. The model chooses a recipient and a verb. This program does the filesystem steps."""

import errno
import fcntl
import getopt
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


def parse_key(text, line_base=0):
    messages, blanked = extract_messages(text)
    nodes = {}
    verbs = {}  # (src, dst) -> [words]
    for lineno, raw in enumerate(blanked.splitlines(), line_base + 1):
        line = raw.strip()
        if not line or line in {"/*", "*/"} or line.startswith("#") or line.startswith("//"):
            continue
        parts = line.split()
        if parts[0] == "node" and len(parts) == 3:
            name, inbox = parts[1], parts[2]
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
            die(f"line {lineno}: expected 'node NAME PATH', 'verb SRC DST WORD...', or 'message NAME {{ ... }}'")
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
        found, rest = getopt.gnu_getopt(argv, "n", ["dry-run", "whatineedtoknow"])
    except getopt.GetoptError as exc:
        die(str(exc))
    dry_run = False
    what = False
    for opt, _value in found:
        if opt in {"-n", "--dry-run"}:
            dry_run = True
        elif opt == "--whatineedtoknow":
            what = True
    return dry_run, what, rest


def report_edges(plan_path, edges):
    say(f"OK {plan_path.resolve()}")
    for src, dst, mode, label in edges:
        extra = f" {label}" if label else ""
        say(f"{src} -> {dst} {mode}{extra}")


def emit_what(me, nodes, verbs, messages, edges):
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
    emit_what(me, nodes, verbs, messages, edges)


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
        emit_what(me, nodes, verbs, messages, edges)
    if dry_run or what:
        return
    old = os.umask(0o002)
    try:
        refs = plan_path.parent / "refs"
        refs.mkdir(parents=True, exist_ok=True)
        try_chmod(refs, 0o2775)
        inbox = nodes[me]
        inbox.mkdir(parents=True, exist_ok=True)
        node = node_dir(inbox)
        try_chmod(node, 0o2750)
        try_chmod(inbox, 0o3775)
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
    files = message_names(inbox)
    oldest = files[0].name if files else None
    if oldest == shown:
        return shown
    if shown and (inbox / shown).is_file() and oldest == shown:
        return shown
    if oldest is None:
        return None
    if shown and (inbox / shown).is_file():
        return shown
    say(f"FILE {inbox / oldest}")
    return oldest


def watch_oldest(inbox, shown):
    while True:
        shown = announce(inbox, shown)
        time.sleep(POLL_SECONDS)


def cmd_arm(plan_path, me):
    plan_path, nodes, _verbs, _messages, _edges = load_plan(plan_path)
    require_me(me, nodes, plan_path)
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


def cmd_send(plan_path, me, recipient, verb, body, refs):
    plan_file, nodes, verbs, messages, edges = load_plan(plan_path)
    require_me(me, nodes, plan_file)
    if recipient not in nodes:
        die(f"unknown node (recipient={recipient})")
    found = edge_between(edges, me, recipient)
    if found is None:
        die(f"{me} is not allowed to send to {recipient}")
    _src, _dst, mode, _label = found
    allowed = verbs.get((me, recipient), [])
    if verb not in allowed:
        die(f"verb {verb} is not allowed on {me} -> {recipient}")
    body = Path(body)
    if not body.is_file():
        die(f"no body file {body}")
    check_body(verb, messages[verb]["body"], body.read_text())
    published = []
    for ref in refs:
        source = Path(ref)
        if not source.is_file():
            die(f"no referenced file {source}")
        published.append(source)
    require_dir(nodes[me], f"run channel-setup {plan_file} {me}")
    inbox = nodes[recipient]
    require_dir(inbox, f"run channel-setup {plan_file} {recipient}")
    if mode == "wait":
        pending = [entry for entry in message_names(inbox) if parse_message_name(entry.name)[0] == me]
        for entry in pending:
            wait_until_removed(inbox, entry.name)
    for source in published:
        if not source.is_file():
            die(f"no referenced file {source}")
    seq_path = out_seq_path(nodes[me])
    handle = lock_exclusive(seq_path.with_suffix(".lock"))
    try:
        current = read_map(seq_path).get("next", 0) + 1
        write_map(seq_path, {"next": current})
    finally:
        handle.close()
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
    text = "\n".join(header) + "\n" + body.read_text()
    if not text.endswith("\n"):
        text += "\n"
    filename = f"{stamp}__{me}__{current:04d}.md"
    tmp = node_dir(nodes[me]) / f".tmp-{filename}"
    tmp.write_text(text)
    try_chmod(tmp, 0o660)
    dest = inbox / filename
    publish_replace(tmp, dest)
    say(f"SENT {dest}")


def main(argv):
    try:
        sys.stdout.reconfigure(line_buffering=True)
    except (AttributeError, OSError):
        pass
    if len(argv) < 2:
        die("usage: channel.py setup|arm|send ...")
    command = argv[1]
    dry_run, what, args = split_args(argv[2:])
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
        if len(args) < 5:
            die("usage: channel-send FILE NODE RECIPIENT VERB BODYFILE [REF ...]")
        cmd_send(args[0], args[1], args[2], args[3], args[4], args[5:])
    else:
        die(f"unknown command {command}")


if __name__ == "__main__":
    try:
        main(sys.argv)
    except PlanError as exc:
        die(str(exc))

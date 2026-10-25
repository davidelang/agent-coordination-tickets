# Scripts

`channel` is the program. `channel-setup`, `channel-arm`, `channel-send`, `channel-ping`, and `channel-status` are symlinks to it. The name selects the mode. `channel` with no mode flags prints help and exits. `--setup`, `--arm`, `--send`, `--ping`, and `--status` select the mode when the name is `channel`. The same layout exists for the shell script here and for the Go binary in [../go](../go/).

Each command takes the plan file and a bare node name. Options follow the `getopt_long` rules: `-n` and `--whatineedtoknow` may sit before, among, or after those arguments. `--` ends the options. A name and a different mode flag is an error.

| Script | When |
|---|---|
| `channel-setup FILE NODE [-n\|--dry-run] [--whatineedtoknow]` | Once per node, by a human. Creates that node's inbox and a `refs/` directory next to the plan file, all mode 2775 (setgid, group-writable, no sticky bit). Safe to rerun: on an existing node it prints `node NODE inbox PATH existing`, exits 0, and changes nothing, so a skill can call it whenever the inbox is missing. `-n` checks the plan, prints the directed edges, and creates nothing. |
| `channel-arm FILE NODE [--whatineedtoknow]` | The receiver leaves this running. It prints `READY`, then `FILE <path>` for the oldest message in that inbox. When that file is removed, it prints the new oldest. It does not print a newer file while an older one is still there. |
| `channel-send FILE NODE DEST VERB [BODYFILE\|-\|-m TEXT ...] [REF ...] [--whatineedtoknow]`<br>`channel-send FILE NODE --retract MESSAGE` | The sender. The body comes from BODYFILE, from stdin (`-` or no body argument), or from `-m TEXT` (repeatable like git; each `-m` is one block of lines, blocks joined by a blank line; with `-m`, every positional after VERB is a REF). A message may carry several REF files. Checks `VERB` is allowed on that arrow and that the body matches the message block. Copies refs into `refs/`, then renames the message into the destination inbox. On a `->` arrow it waits (`PENDING`) until this sender's previous file there is removed; on a `-<` arrow it sends at once. The destination inbox has to exist already. It ends with `DELIVERED PATH` once the file is in the destination inbox; see Send and retract. `--retract` takes a sent message back. `--timeout` is only for `DEST PING`. |
| `channel-ping FILE NODE [--all [--every MIN]] [--run ID] [--timeout SEC]` | Connectivity test. Sends a built-in PING on every arrow out of NODE. Where DEST has an arrow back it waits only for the PONG and prints `PONG DEST 0.2s` or `TIMEOUT DEST (...)` (exit 1). Where DEST has no arrow back no PONG is possible: it prints `NO-RETURN DEST (the PING is delivered; ...)` and does not wait. An explicit ping is never rate-limited. `--all` starts a check-all run that spreads over every link (see [Check-all](#check-all-links)); `--run ID` is how a receiving node joins a run. `--timeout` defaults to 300s (env `CHANNEL_PING_TIMEOUT`). |
| `channel-status FILE` | One line per node: `node NAME INBOX messages N [oldest AGEs] claimed M`, then `  claimed FILE AGEs [owner=...]` for each claimed file. An inbox it cannot read prints `no permission` (or `missing`). |

`--whatineedtoknow` starts with a "how to act" header filled in for that node: the absolute plan and inbox paths, the exact `channel-arm` watch and `channel-send` commands (the running binary's siblings), what goes in the body file, handling the oldest file first, and removing a file from the inbox when done. Then it prints the nodes this one talks to and the `message` blocks on those arrows, and exits. A bot needs nothing beyond that output. It does not create directories, send, or watch. Run it on `channel-arm` after a start or a compact. The flag is on all three so the dump is not a fourth command.

The header is the agent's whole instruction set and is written for one agent tool, named by `CHANNEL_AGENT` (the launcher sets it; there is no other signal and no guessing):

- `grok`: start `channel-arm PLAN NODE` with the monitor tool (`persistent: true`).
- `cursor`: run `.cursor/lib/cursor-channel-watch PLAN NODE` (must exist under the current directory, the repo root) as a background shell that wakes on `^FILE `.

Unset or any other value: `--whatineedtoknow` prints `CHANNEL-AGENT-UNKNOWN: ...` on stderr, nothing on stdout, and exits 1. The header also lists the node's duties (verbs it may send and receive on each arrow), says to answer PING itself, to keep its current work, that a fresh session needs an explicit `/channel`, that a message never authorizes push or deploy, how to report BLOCKED, to run sends that may wait (`PENDING`) in the background so two waiting senders cannot deadlock, that a send is done at `DELIVERED` (never check DEST's inbox), and that a claim that fails because the sender took the file back is a lost race: say so and keep watching.

The receiver removes a finished message with `rm` of that one path. There is no `channel-finish`.

The script chooses the filename: `YYYYMMDD-HHMMSS.mmm__SENDER__SEQ.md`. `SEQ` is allocated without locks: the sender creates `out-seq.d/N` beside its inbox with an exclusive create (O_EXCL / noclobber); on a collision it tries N+1. A crash or timeout can at worst skip a number, and nothing is ever left locked. `out-seq.txt` is only a hint (last number used) so the search starts near the end. The agent does not pick the name, and a later send does not reuse an earlier number. Messages this sender has not had removed are the files still sitting in the other inbox.

There is no claim step and no cursor. The file stays in the inbox until the receiver removes it. A restart of `channel-arm` prints that same oldest file again, so the work has to be safe to run twice.

## PING and PONG (built in)

Every arrow carries PING and PONG without a `verb` or `message` line. The body is `NONCE: <value>`; PONG echoes the PING's nonce. A PING or PONG with a NONCE line never waits on a `->` arrow.

## Send and retract

A send prints `SENT PATH` and ends with one line once the file is in DEST's inbox. It never waits for DEST to take or handle the message, and the sender does not check DEST's inbox, `claimed/` or `channel-status` for that; an answer comes back as a message.

| Line | Exit | Meaning |
|---|---|---|
| `DELIVERED PATH` | 0 | The file is in DEST's inbox. The send is done. A signal after this point still ends with `DELIVERED`. |
| `ABORTED DEST VERB (not sent: ...)` | 4 | Stopped by a signal while `PENDING` on a `->` arrow; the message never reached DEST's inbox. |

`channel-send FILE NODE --retract MESSAGE` (MESSAGE = the `SENT` path, or just its file name) renames the file from DEST's inbox into `nodes/SENDER/retracted/`. DEST's claim is also a single rename (or unlink), so exactly one of them wins. Both results exit 0:

| Line | Meaning |
|---|---|
| `RETRACTED PATH -> nodes/SENDER/retracted/FILE (--retract)` | Taken back; DEST never got it. A repeated retract prints `(already retracted)`. |
| `NOT-RETRACTED PATH (DEST already has it)` | DEST got it first; the sender lost the race. |

A file name that is not SENDER's, a path outside the inboxes of SENDER's arrows, or a message SENDER never sent fails. On the receiving side, an `mv` or `rm` of a `FILE` that says `No such file` means the sender took it back first: the agent says so in one line and keeps watching. `CHANNEL_SEND_WAIT` was removed; setting it at all is an error.

Run a send that may wait (`PENDING`) in the background (agents: as `--whatineedtoknow` says for the tool) and keep handling the inbox; on `->` arrows two nodes that wait on each other otherwise deadlock.

A PING is an ordinary message: `channel-arm` shows it as `FILE` and nothing answers it for the agent. The agent replies PONG with `channel-send` (echoing the NONCE) and removes the PING, so a PONG proves that agent was woken and acted. On a one-way arrow nothing can come back, so `channel-ping` reports `NO-RETURN` once the PING is delivered. A PONG is not shown: `channel-arm` moves it to `nodes/NODE/pong-NONCE` for `channel-ping` to collect (with no watch, `channel-ping` takes it from the inbox itself). A diag may still define PING/PONG blocks (see the commented example in `example/comms.diag`).

`channel-send FILE NODE DEST PING [-m 'NONCE: V']` runs the same code as `channel-ping`, limited to DEST: it sends the PING (body built from the plan's PING block; the given body may hold only `NONCE:` and lines of that block, else `CHANNEL-SEND-PING`), waits for the PONG (`--timeout` / `CHANNEL_PING_TIMEOUT`, default 300s) and prints `PONG DEST SECS` or `TIMEOUT DEST (...)` with exit 1; with no arrow back from DEST it prints `NO-RETURN DEST (...)` (exit 0) without waiting. So a hand-sent PING always gets its answer. `channel-send ... DEST PONG` is refused (`CHANNEL-SEND-PONG`) unless a PING from DEST with that NONCE is still in NODE's inbox or `claimed/`: send the PONG, then remove the PING.

When the diag defines a PING or PONG block, every body the tools build themselves (the PING from `channel-ping`, with its check-all `SCOPE`/`RUN` lines) follows that block: literal lines such as `ACTION: PONG` are copied, and each `KEY: <...>` line is filled by its key. The tools fill only `NONCE`, `SCOPE`, and `RUN`; keys the block does not mention are added as extra lines (extra lines are allowed). Any other `<...>` line in the block is an error that starts with `CHANNEL-CONTROL-BODY:` and names the line. Without a block the body is just those `KEY: value` lines.

The agent's PONG is checked against the PONG block like any send. `channel-ping` with a block it cannot fill fails before sending anything and logs `event=ping result=fail`. When no PONG arrives, `channel-ping` prints `TIMEOUT DEST (no PONG after Ns: the DEST agent was not woken or did not answer)` and exits 1. A one-way arrow never times out: it prints `NO-RETURN`.

## Check-all (links)

`channel-ping --all FILE NODE` checks every link reachable from NODE, not just its own arrows. The origin makes a run id (`NODE-YYYYmmdd-HHMMSS-PID`, printed as `CHECKALL NODE RUN`) and sends PINGs whose body is

```
NONCE: <value>
SCOPE: all
RUN: <run id>
```

A node that receives such a PING answers PONG as usual and then starts `channel-ping --run RUN FILE NODE` in the background, which pings its own outgoing arrows with the same RUN. The agent does this (its `--whatineedtoknow` header says so); the watch never does.

Propagation is what could loop, so `channel-ping --run` (never a plain ping) stops early, printing `SKIP`, in two cases:

- this node already handled that run (run ids are recorded in `nodes/NODE/checkall-runs`, and the origin records its own run, so the wave coming back stops there), or
- this node ran a check-all within the last `--every` minutes (default 30, env `CHANNEL_CHECKALL_EVERY`; stamp `nodes/NODE/checkall.stamp`).

There are no locks; two waves racing on one node at worst both propagate once. Each node logs its own results (`event=ping ... run=RUN`) plus `event=checkall result=start|skip`; the origin also prints them. The origin waits only for its own arrows; results from further nodes are in syslog:

```
journalctl -t agent-channel | grep 'run=planner-20261014-'   # every result of one run
```

## Logging

Every send, delivery (`FILE` announced by `channel-arm`), consumption (announced file removed), PING answered, PONG received, ping result, and webhook result is logged as one `key=value` line to syslog with tag `agent-channel` (facility user; failures and timeouts at warning). `CHANNEL_SYSLOG=0` turns syslog off; `CHANNEL_LOG_FILE=/path` also appends each line, with a timestamp, to that file.

```
event=send plan=/p/x.diag from=planner to=coder verb=IMPLEMENT seq=12 refs=2 file=/p/nodes/coder/inbox/...
event=deliver node=coder file=...            event=consumed node=coder file=...
event=webhook result=ok|fail|retrying to=review from=planner verb=REVIEW attempts=2 [error="..."] file=...
event=ping result=ok|timeout from=planner to=lab kind=pong|consumed seconds=0.4s [run=RUN]
event=checkall result=start|skip|fail node=lab run=RUN [why="run already handled"|"rate limit"]
```

Example alert queries:

```
journalctl -t agent-channel -p warning --since -1h            # any webhook failure or ping timeout
journalctl -t agent-channel --since -1h | grep 'result=fail'  # webhook failures only
# nothing consumed for a node in 6 hours while it has messages:
journalctl -t agent-channel --since -6h | grep -c 'event=consumed node=coder'   # 0 -> alert
channel-status PLAN | awk '$5 > 0 && $7+0 > 21600'           # oldest message older than 6h
```

## Webhooks (optional)

A `node` line may name a webhook to fire after each message queued to that node. This is for a receiver that cannot block on `channel-arm`, for example a bot whose routine is started by a webhook. The webhook belongs to one workflow, so it lives only in that workflow's diag file, never in project or system config. The shell script (`bin/channel`) and the Go binary (`go/channel`) behave the same.

```
node review /abs/inbox webhook=https://api2.cursor.sh/automations/webhook/<id> webhook_key=<token>
node review /abs/inbox webhook=https://api2.cursor.sh/automations/webhook/<id> webhook_key_file=review.key
node review /abs/inbox webhook=http://127.0.0.1:8765/hook                # no Authorization header
```

- Options are `key=value` after the inbox path. The allowed keys are `webhook`, `webhook_key`, `webhook_key_file`, `webhook_retries`, and `webhook_backoff_max`. Any other key is an error. Values may not contain spaces.
- `webhook` must start with `http://` or `https://`. Give at most one of the two key options, and a key needs `webhook`.
- `webhook_key=<token>` inline and `webhook_key_file=<path>` are both fully supported. A relative `webhook_key_file` is resolved from the diag file's directory, so a key file can sit next to the plan; an absolute path also works. The file's first line is the key.
- Give only the key. The tool builds `Authorization: Bearer <key>`. A leading `Authorization: Bearer ` or `Bearer ` is stripped, so a pasted header line also works. The key never appears on a command line.
- After the rename into the inbox, `channel-send` POSTs (connect timeout 5s, total 15s) with `Content-Type: application/json` and the body `{"target":NODE,"from":SENDER,"verb":VERB,"inbox":INBOX,"file":PATH}`.
- Retries: `channel-send` makes the first POST itself. If it fails with anything but 401, 403, 404, 405 or 410 (a wrong key or URL), including a 400 (some hosts report their rate limit as 400), the send prints a `WARNING ... retrying in the background` line and returns at once; a detached background process (own session, survives the sender) retries with exponential backoff (1s, 2s, 4s, ... capped at `webhook_backoff_max`, default `30m`) up to `webhook_retries` more times (default 12, so the schedule reaches the 30m cap and spans about an hour; `0` disables retries). `webhook_backoff_max` takes `N` (seconds), `Ns`, `Nm` or `Nh`. 401, 403, 404, 405 and 410 are not retried. The first failure is logged as `result=retrying`; the background job logs the final `result=ok` or `result=fail` with `attempts=`.
- On a 2xx it prints `WEBHOOK NAME CODE RESPONSE`. On any other first result (non-2xx, unreachable, unreadable key file, timeout) it prints a `WARNING:` line on stderr. The message stays queued and the exit status is unchanged.
- Nodes without a webhook behave exactly as before. `--whatineedtoknow` does not print webhook URLs or keys.

## One watch per node

`channel-arm` records itself in `nodes/NODE/arm.pid` (`PID HOST`). If that file names a live `channel-arm` on this host, a second `channel-arm` for the same node prints `ALREADY-WATCHING NODE pid PID (...)` and exits with status 3 instead of starting; the running watch keeps going. A pid that is gone or is not a `channel-arm` does not block. A pid recorded by another host cannot be checked, so it is reported the same way; remove the file if that watch is gone.

## No fallbacks

Each step has one defined path; when it cannot run, the command fails with a message instead of quietly doing something else.

- `inotifywait` is required. If it is missing, exits at start, or stops while running, `channel-arm` / `channel-send` exit with an error. `CHANNEL_POLL=1` is the only way to list the inbox every 0.2s instead, and it must be set on purpose.
- Numbers in `CHANNEL_CHECKALL_EVERY` / `CHANNEL_PING_TIMEOUT` (and `--every` / `--timeout`) must be whole numbers; anything else is an error (unset or empty means the default).
- A syslog or `CHANNEL_LOG_FILE` write that fails prints a `WARNING` on stderr (set `CHANNEL_SYSLOG=0` to turn syslog off deliberately).
- A check-all that cannot be passed on (bad `RUN`, cannot start, cannot record the run) is printed and logged as `event=checkall result=fail`; a node that cannot record a run does not pass it on.
- A PONG that cannot be filed, or a sent file whose mode cannot be set, prints a note.
- The shell version needs `setsid` (util-linux, like GNU `getopt`).

## In-progress files and restarts

`channel-arm` only announces message files directly inside `inbox/`; subdirectories are ignored. A node may move a message it is working on into `inbox/claimed/` (create it with mode 2775) so the watch moves on. Remove the claimed file only after the reply to it has been sent.

On a start or after a compaction, a node first reviews `inbox/claimed/`: those are messages it already started and may have partly finished. Before redoing anything it checks whether its reply was already sent (for example in the destination inbox, or its own notes). Claims are never reclaimed automatically by age; `channel-status` reports them with their ages so stale ones can be handled by a person. A node may write an optional sidecar `claimed/FILE.owner` (first line: who has it); `channel-status` shows it as `owner=`.

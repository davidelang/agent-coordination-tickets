# Scripts

`channel` is the program. `channel-setup`, `channel-arm`, `channel-send`, `channel-ping`, and `channel-status` are symlinks to it. The name selects the mode. `channel` with no mode flags prints help and exits. `--setup`, `--arm`, `--send`, `--ping`, and `--status` select the mode when the name is `channel`. The same layout exists for the shell script here and for the Go binary in [../go](../go/).

Each command takes the plan file and a bare node name. Options follow the `getopt_long` rules: `-n` and `--whatineedtoknow` may sit before, among, or after those arguments. `--` ends the options. A name and a different mode flag is an error.

| Script | When |
|---|---|
| `channel-setup FILE NODE [-n\|--dry-run] [--whatineedtoknow]` | Once per node, by a human. Creates that node's inbox and a `refs/` directory next to the plan file, all mode 2775 (setgid, group-writable, no sticky bit). Safe to rerun: on an existing node it prints `node NODE inbox PATH existing`, exits 0, and changes nothing, so a skill can call it whenever the inbox is missing. `-n` checks the plan, prints the directed edges, and creates nothing. |
| `channel-arm FILE NODE [--whatineedtoknow]` | The receiver leaves this running. It prints `READY`, then `FILE <path>` for the oldest message in that inbox. When that file is removed, it prints the new oldest. It does not print a newer file while an older one is still there. |
| `channel-send FILE NODE DEST VERB [BODYFILE\|-\|-m TEXT ...] [REF ...] [--whatineedtoknow]` | The sender. The body comes from BODYFILE, from stdin (`-` or no body argument), or from `-m TEXT` (repeatable like git; each `-m` is one block of lines, blocks joined by a blank line; with `-m`, every positional after VERB is a REF). A message may carry several REF files. Checks `VERB` is allowed on that arrow and that the body matches the message block. Copies refs into `refs/`, then renames the message into the destination inbox. On a `->` arrow it waits until this sender's previous file there is removed. The destination inbox has to exist already. |
| `channel-ping FILE NODE [--force] [--every MIN] [--timeout SEC]` | Connectivity test. Sends a built-in PING on every arrow out of NODE. For a two-way link it waits for the PONG; for a one-way link it waits until the PING is removed (consumed). Prints `PONG DEST 0.2s`, `CONSUMED DEST 0.2s`, or `TIMEOUT DEST (...)` (exit 1). Rate-limited: skipped with `SKIP` if NODE pinged within `--every` minutes (default 30, env `CHANNEL_PING_EVERY`; stamp `nodes/NODE/ping.stamp`). `--force` ignores the limit; `--timeout` defaults to 300s (env `CHANNEL_PING_TIMEOUT`). |
| `channel-status FILE` | One line per node: `node NAME INBOX messages N [oldest AGEs] claimed M`, then `  claimed FILE AGEs [owner=...]` for each claimed file. An inbox it cannot read prints `no permission` (or `missing`). |

`--whatineedtoknow` starts with a "how to act" header filled in for that node: the absolute plan and inbox paths, the exact `channel-arm` watch and `channel-send` commands (the running binary's siblings), what goes in the body file, handling the oldest file first, and removing a file from the inbox when done. Then it prints the nodes this one talks to and the `message` blocks on those arrows, and exits. A bot needs nothing beyond that output. It does not create directories, send, or watch. Run it on `channel-arm` after a start or a compact. The flag is on all three so the dump is not a fourth command.

The receiver removes a finished message with `rm` of that one path. There is no `channel-finish`.

The script chooses the filename: `YYYYMMDD-HHMMSS.mmm__SENDER__SEQ.md`. `SEQ` is allocated without locks: the sender creates `out-seq.d/N` beside its inbox with an exclusive create (O_EXCL / noclobber); on a collision it tries N+1. A crash or timeout can at worst skip a number, and nothing is ever left locked. `out-seq.txt` is only a hint (last number used) so the search starts near the end. The agent does not pick the name, and a later send does not reuse an earlier number. Messages this sender has not had removed are the files still sitting in the other inbox.

There is no claim step and no cursor. The file stays in the inbox until the receiver removes it. A restart of `channel-arm` prints that same oldest file again, so the work has to be safe to run twice.

## PING and PONG (built in)

Every arrow carries PING and PONG without a `verb` or `message` line. The body is `NONCE: <value>`; PONG echoes the PING's nonce. A PING or PONG with a NONCE line never waits on a `->` arrow.

`channel-arm` handles them transparently: on a PING it sends PONG back (when the reverse arrow exists) and removes the PING; on a PONG it moves the file to `nodes/NODE/pong-NONCE` for `channel-ping` to collect. Neither is ever printed as `FILE`, so a watched agent is not woken. A node with no watch (for example a webhook-woken bot) sees the PING as a normal file and answers it by hand, as its `--whatineedtoknow` header says. A diag may still define PING/PONG blocks (see the commented example in `example/comms.diag`); a PING without a NONCE line is treated as an ordinary message.

## Logging

Every send, delivery (`FILE` announced by `channel-arm`), consumption (announced file removed), PING answered, PONG received, ping result, and webhook result is logged as one `key=value` line to syslog with tag `agent-channel` (facility user; failures and timeouts at warning). `CHANNEL_SYSLOG=0` turns syslog off; `CHANNEL_LOG_FILE=/path` also appends each line, with a timestamp, to that file.

```
event=send plan=/p/x.diag from=planner to=coder verb=IMPLEMENT seq=12 refs=2 file=/p/nodes/coder/inbox/...
event=deliver node=coder file=...            event=consumed node=coder file=...
event=webhook result=ok|fail to=review from=planner verb=REVIEW attempts=2 [error="..."] file=...
event=ping result=ok|timeout from=planner to=lab kind=pong|consumed seconds=0.4s
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
- Retries: a network error, timeout, 5xx, or 429 is retried with exponential backoff (1s, 2s, 4s, ... capped at `webhook_backoff_max` seconds, default 30) up to `webhook_retries` times (default 3; `0` disables). Other 4xx results are not retried. Each retry prints a note on stderr.
- On a 2xx it prints `WEBHOOK NAME CODE RESPONSE`. On any other result (non-2xx, unreachable, unreadable key file, timeout) it prints a `WARNING:` line on stderr. The message stays queued and the exit status is unchanged.
- Nodes without a webhook behave exactly as before. `--whatineedtoknow` does not print webhook URLs or keys.

## In-progress files and restarts

`channel-arm` only announces message files directly inside `inbox/`; subdirectories are ignored. A node may move a message it is working on into `inbox/claimed/` (create it with mode 2775) so the watch moves on. Remove the claimed file only after the reply to it has been sent.

On a start or after a compaction, a node first reviews `inbox/claimed/`: those are messages it already started and may have partly finished. Before redoing anything it checks whether its reply was already sent (for example in the destination inbox, or its own notes). Claims are never reclaimed automatically by age; `channel-status` reports them with their ages so stale ones can be handled by a person. A node may write an optional sidecar `claimed/FILE.owner` (first line: who has it); `channel-status` shows it as `owner=`.

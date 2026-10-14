# Scripts

`channel` is the program. `channel-setup`, `channel-arm`, and `channel-send` are symlinks to it. The name selects the mode. `channel` with no mode flags prints help and exits. `--setup`, `--arm`, and `--send` select the mode when the name is `channel`. The same layout exists for the shell script here and for the Go binary in [../go](../go/).

Each command takes the plan file and a bare node name. Options follow the `getopt_long` rules: `-n` and `--whatineedtoknow` may sit before, among, or after those arguments. `--` ends the options. A name and a different mode flag is an error.

| Script | When |
|---|---|
| `channel-setup FILE NODE [-n\|--dry-run] [--whatineedtoknow]` | Once per node, by a human. Creates that node's inbox and a `refs/` directory next to the plan file, all mode 2775 (setgid, group-writable, no sticky bit). `-n` checks the plan, prints the directed edges, and creates nothing. |
| `channel-arm FILE NODE [--whatineedtoknow]` | The receiver leaves this running. It prints `READY`, then `FILE <path>` for the oldest message in that inbox. When that file is removed, it prints the new oldest. It does not print a newer file while an older one is still there. |
| `channel-send FILE NODE DEST VERB BODY [REF ...] [--whatineedtoknow]` | The sender. Checks `VERB` is allowed on that arrow and that `BODY` matches the message block. Copies refs into `refs/`, then renames the message into the destination inbox. On a `->` arrow it waits until this sender's previous file there is removed. The destination inbox has to exist already. |

`--whatineedtoknow` starts with a "how to act" header filled in for that node: the absolute plan and inbox paths, the exact `channel-arm` watch and `channel-send` commands (the running binary's siblings), what goes in the body file, handling the oldest file first, and removing a file from the inbox when done. Then it prints the nodes this one talks to and the `message` blocks on those arrows, and exits. A bot needs nothing beyond that output. It does not create directories, send, or watch. Run it on `channel-arm` after a start or a compact. The flag is on all three so the dump is not a fourth command.

The receiver removes a finished message with `rm` of that one path. There is no `channel-finish`.

The script chooses the filename: `YYYYMMDD-HHMMSS.mmm__SENDER__SEQ.md`. `SEQ` comes from a counter the script keeps beside the sender's inbox, in `out-seq.txt`. The agent does not pick the name, and a later send does not reuse an earlier number. Messages this sender has not had removed are the files still sitting in the other inbox.

There is no claim step and no cursor. The file stays in the inbox until the receiver removes it. A restart of `channel-arm` prints that same oldest file again, so the work has to be safe to run twice.

## Webhooks (optional)

A `node` line may name a webhook to fire after each message queued to that node. This is for a receiver that cannot block on `channel-arm`, for example a bot whose routine is started by a webhook. The webhook belongs to one workflow, so it lives only in that workflow's diag file, never in project or system config. The shell script (`bin/channel`) and the Go binary (`go/channel`) behave the same.

```
node review /abs/inbox webhook=https://api2.cursor.sh/automations/webhook/<id> webhook_key=<token>
node review /abs/inbox webhook=https://api2.cursor.sh/automations/webhook/<id> webhook_key_file=review.key
node review /abs/inbox webhook=http://127.0.0.1:8765/hook                # no Authorization header
```

- Options are `key=value` after the inbox path. The allowed keys are `webhook`, `webhook_key`, and `webhook_key_file`. Any other key is an error. Values may not contain spaces.
- `webhook` must start with `http://` or `https://`. Give at most one of the two key options, and a key needs `webhook`.
- `webhook_key=<token>` inline and `webhook_key_file=<path>` are both fully supported. A relative `webhook_key_file` is resolved from the diag file's directory, so a key file can sit next to the plan; an absolute path also works. The file's first line is the key.
- Give only the key. The tool builds `Authorization: Bearer <key>`. A leading `Authorization: Bearer ` or `Bearer ` is stripped, so a pasted header line also works. The key never appears on a command line.
- After the rename into the inbox, `channel-send` POSTs (connect timeout 5s, total 15s) with `Content-Type: application/json` and the body `{"target":NODE,"from":SENDER,"verb":VERB,"inbox":INBOX,"file":PATH}`.
- On a 2xx it prints `WEBHOOK NAME CODE RESPONSE`. On any other result (non-2xx, unreachable, unreadable key file, timeout) it prints a `WARNING:` line on stderr. The message stays queued and the exit status is unchanged.
- Nodes without a webhook behave exactly as before. `--whatineedtoknow` does not print webhook URLs or keys.

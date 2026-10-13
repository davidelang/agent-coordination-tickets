# Scripts

`channel` is the program. `channel-setup`, `channel-arm`, and `channel-send` are symlinks to it. The name selects the mode. `channel` with no mode flags prints help and exits. `--setup`, `--arm`, and `--send` select the mode when the name is `channel`. The same layout exists for the shell script here and for the Go binary in [../go](../go/).

Each command takes the plan file and a bare node name. Options follow the `getopt_long` rules: `-n` and `--whatineedtoknow` may sit before, among, or after those arguments. `--` ends the options. A name and a different mode flag is an error.

| Script | When |
|---|---|
| `channel-setup FILE NODE [-n\|--dry-run] [--whatineedtoknow]` | Once per node, by a human. Creates that node's inbox and a `refs/` directory next to the plan file. `-n` checks the plan, prints the directed edges, and creates nothing. |
| `channel-arm FILE NODE [--whatineedtoknow]` | The receiver leaves this running. It prints `READY`, then `FILE <path>` for the oldest message in that inbox. When that file is removed, it prints the new oldest. It does not print a newer file while an older one is still there. |
| `channel-send FILE NODE DEST VERB BODY [REF ...] [--whatineedtoknow]` | The sender. Checks `VERB` is allowed on that arrow and that `BODY` matches the message block. Copies refs into `refs/`, then renames the message into the destination inbox. On a `->` arrow it waits until this sender's previous file there is removed. The destination inbox has to exist already. |

`--whatineedtoknow` prints the nodes this one talks to and the `message` blocks on those arrows, then exits. It does not create directories, send, or watch. Run it on `channel-arm` after a start or a compact. The flag is on all three so the dump is not a fourth command.

The receiver removes a finished message with `rm` of that one path. There is no `channel-finish`.

The script chooses the filename: `YYYYMMDD-HHMMSS.mmm__SENDER__SEQ.md`. `SEQ` comes from a counter the script keeps beside the sender's inbox, in `out-seq.txt`. The agent does not pick the name, and a later send does not reuse an earlier number. Messages this sender has not had removed are the files still sitting in the other inbox.

There is no claim step and no cursor. The file stays in the inbox until the receiver removes it. A restart of `channel-arm` prints that same oldest file again, so the work has to be safe to run twice.

## Webhooks (optional)

A `node` line may name a webhook to fire after each message queued to that node. This is for a receiver that cannot block on `channel-arm`, for example a bot whose routine is started by a webhook. The webhook belongs to one workflow, so it lives only in that workflow's diag file, never in project or system config.

```
node review /abs/inbox webhook=https://api2.cursor.sh/automations/webhook/<id> webhook_key_file=/abs/path/review.key
node review /abs/inbox webhook=https://example/hook webhook_key=<token>    # inline; avoid
node review /abs/inbox webhook=http://127.0.0.1:8765/hook                # no auth header
```

- Options are `key=value` after the inbox path. The allowed keys are `webhook`, `webhook_key_file`, and `webhook_key`. Any other key is an error. Values may not contain spaces.
- `webhook` must start with `http://` or `https://`. `webhook_key_file` must be absolute. Give at most one of the two key options, and a key needs `webhook`.
- The key file's first line is the token. A leading `Bearer ` or `Authorization: Bearer ` is stripped. Keep it mode 0600 and outside any repo. The key is passed to curl on stdin, never on the command line.
- After the rename into the inbox, `channel-send` runs `curl -sS --connect-timeout 5 --max-time 15 -X POST` with `Content-Type: application/json`, `Authorization: Bearer <key>`, and the body `{"target":NODE,"from":SENDER,"verb":VERB,"inbox":INBOX,"file":PATH}`.
- On a 2xx it prints `WEBHOOK NAME CODE RESPONSE`. On any other result (non-2xx, no curl, unreadable key file, timeout) it prints a `WARNING:` line on stderr. The message stays queued and the exit status is unchanged.
- Nodes without a webhook behave exactly as before. `--whatineedtoknow` does not print webhook URLs or keys.

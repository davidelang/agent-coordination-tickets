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

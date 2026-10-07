# Agent channel

One inbox directory per agent. A sender renames a finished file into that inbox. The receiver handles the oldest file there and deletes it when the work is done.

## The problem

Agents that poll a mailbox spend their context window on the waiting. A long-running script watches one directory and wakes the agent only when a file is ready. A blockdiag file says who may speak, and whether a second message waits. Message blocks in that same file say what each word means and what the body looks like.

## Dependencies

Two programs implement the same protocol.

`bin/channel` is a `/bin/sh` script. `channel-setup`, `channel-arm`, and `channel-send` are links to it. It needs GNU `getopt`, `awk`, `flock`, `stat`, and `date`. `inotifywait` is optional. Without it, or with `CHANNEL_POLL=1`, the watch lists the directory every 0.2 seconds and still prints a line only when the oldest file changes.

`go/channel` is a static binary built from the Go standard library only. The same three names are links to that binary. Build it with the command in [go/README.md](go/README.md). The agent runs that binary, so a permission grant keys off the program name.

`bin/channel.py` is an earlier Python form of the same protocol. It is kept for comparison. `tests/run` builds the Go binary and checks the shell program and the Go program against each other, including a file one of them wrote and the other one read.

`blockdiag` is optional. It draws the plan file.

## Quick start

```sh
export PATH="$PWD/bin:$PATH"
# or: export PATH="$PWD/go:$PATH"
cp example/comms.diag /path/to/comms.diag
# replace the /channel prefix in that file
channel-setup -n /path/to/comms.diag planner
channel-setup /path/to/comms.diag planner
channel-setup /path/to/comms.diag coder
```

`-n` reads the plan and creates nothing. Each `channel-setup` creates that node's inbox and the shared `refs/` directory.

The coder watches:

```sh
channel-arm /path/to/comms.diag coder
```

The planner sends:

```sh
channel-send /path/to/comms.diag planner coder IMPLEMENT body.md
```

When the work is done, the coder removes that one inbox file with `rm`.

`body.md` has to match the `IMPLEMENT` message block. Lines with `<...>` are filled in. The other lines are copied as written. After a restart, `channel-arm /path/to/comms.diag coder --whatineedtoknow` prints the nodes and messages this coder uses, then exits. The same flag works on `channel-setup` and `channel-send`.

## More

| Path | What it is |
|---|---|
| [example/comms.diag](example/comms.diag) | A planner and a coder. Copy this and change the paths. |
| [bin/README.md](bin/README.md) | What each script is for. |
| [docs/plan-file.md](docs/plan-file.md) | `node`, `verb`, and `message` syntax. |
| [docs/diagram-marks.md](docs/diagram-marks.md) | Why the arrow operators are `->`, `<->`, and `-<`. |
| [prompts/](prompts/) | Text to give each agent, and how to grant the scripts. |

The license is [MIT](LICENSE).

# Agent channel

One inbox directory per agent. A sender renames a finished file into that inbox. The receiver handles the oldest file there and deletes it when the work is done.

## The problem

Agents that poll a mailbox spend their context window on the waiting. A long-running script watches one directory and wakes the agent only when a file is ready. A blockdiag file says who may speak, and whether a second message waits (`->`, one in flight) or queues (`-<`). Message blocks in that same file say what each word means and what the body looks like.

## Dependencies

Two programs implement the same protocol.

`bin/channel` is a `/bin/sh` script. `channel-setup`, `channel-arm`, `channel-send`, `channel-ping`, `channel-status`, and `channel-rebind` are links to it. It needs GNU `getopt`, `setsid`, `awk`, `stat`, and `date`. It also needs `inotifywait` (inotify-tools); without it `channel-arm` and a waiting `channel-send` exit with an error rather than switching to something else. `CHANNEL_POLL=1` is the one deliberate alternative: the watch lists the directory every 0.2 seconds and still prints a line only when the oldest file changes. If `inotifywait` stops while running, the command exits with an error.

`go/channel` is a static binary built from the Go standard library only. The same names are links to that binary. Build it with the command in [go/README.md](go/README.md). The agent runs that binary, so a permission grant keys off the program name.

`bin/channel.py` is an earlier Python form of the same protocol. It is kept for comparison. `tests/run` builds the Go binary and checks the shell program and the Go program against each other, including a file one of them wrote and the other one read.

`blockdiag` is optional. It draws the plan file.

## Quick start

```sh
export PATH="$PWD/bin:$PATH"
# or: export PATH="$PWD/go:$PATH"
# consuming repo: ./setup_agent.sh feature-x  →  checkout in agent-N, symlink feature-x.wt
cp example/comms.diag sandbox/connect/feature-x/config.diag
# replace the /channel prefix with the sandbox absolute path (keep connect/feature-x)
# from that worktree (git branch feature-x):
channel-setup -n planner
channel-setup planner
channel-setup coder
```

`<workspace>` in `sandbox/connect/<workspace>/config.diag` is the descriptive name: the branch given to `setup_agent.sh` (for example `feature-x`) or `master` for that worktree. `setup_agent.sh` places the checkout in `agent-N`; the channel path uses the descriptive name. Node inboxes go under the same directory (`sandbox/connect/<workspace>/nodes/...`). Omitted FILE on setup, arm, ping, and status is `$git-toplevel/sandbox/connect/<git-branch>/config.diag`. `channel-send` still needs the path. The tools also accept any path; this library's example stays `example/comms.diag`.

`-n` reads the plan and creates nothing. Each `channel-setup` creates that node's inbox, `inbox/claimed/`, and the shared `refs/` directory.

The coder watches:

```sh
channel-arm coder
```

After `/new`, take back that leftover watch:

```sh
channel-rebind
```

The planner sends:

```sh
channel-send sandbox/connect/feature-x/config.diag planner coder IMPLEMENT body.md
```

When the work is done, the coder removes that one inbox file with `rm`.

The send prints `SENT PATH`, then `DELIVERED PATH` (exit 0) once the file is in the coder's inbox, and returns. The sender's part ends there; it does not watch whether the coder has taken it. On a `->` arrow a second send prints `PENDING` and waits until the coder removes the first file; stopping it then prints `ABORTED` (nothing sent). `channel-send ... --retract PATH` takes a message back: `RETRACTED` if the coder had not taken it, `NOT-RETRACTED` if it had. See [bin/README.md](bin/README.md#send-and-retract).

`body.md` has to match the `IMPLEMENT` message block. Lines with `<...>` are filled in. The other lines are copied as written. After a restart, `channel-arm coder --whatineedtoknow` prints the nodes and messages this coder uses, then exits. The same flag works on `channel-setup` and `channel-send`.

## More

| Path | What it is |
|---|---|
| [example/comms.diag](example/comms.diag) | A planner and a coder. Copy to `sandbox/connect/<workspace>/config.diag` and replace `/channel`. |
| [bin/README.md](bin/README.md) | What each script is for. |
| [docs/plan-file.md](docs/plan-file.md) | `node`, `verb`, and `message` syntax. |
| [docs/diagram-marks.md](docs/diagram-marks.md) | The arrow marks: `->` (one in flight), `-<` (queue), both on one line (`<->`, `>-<`, `>->`), and what blockdiag draws. |
| [prompts/](prompts/) | Text to give each agent, and how to grant the scripts. |
| [dispatch/](dispatch/README.md) | A headless dispatcher that serves a node by starting one fresh agent per message through a launch hook: executor, reviewer, tester. [docs/dispatch.md](docs/dispatch.md) has the details. |

The license is [MIT](LICENSE).

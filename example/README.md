# Example

A planner and one coder. Each direction is one-at-a-time.

`comms.diag` is the picture and the key in one file. The three lines above the diagram are the arrow rules. The key is inside a `/* */` comment so blockdiag skips it. Inbox paths use the dummy prefix `/channel` (the sandbox) and a workspace directory `connect/feature-x`. Copy the file to `sandbox/connect/<workspace>/config`, replace `/channel` with the sandbox absolute path, and replace `feature-x` if this worktree's descriptive name is different, then `channel-setup -n` and `channel-setup` once per node. From that worktree, `channel-setup planner` and `channel-arm coder` omit the path and use this file.

`<workspace>` is the descriptive name: the branch given to `setup_agent.sh` (for example `feature-x`) or `master` for that worktree. `setup_agent.sh` checks out into `agent-N` and links `<workspace>.wt` there. See [../docs/plan-file.md](../docs/plan-file.md).

```sh
channel-setup -n planner
channel-setup planner
channel-setup coder
channel-arm coder
channel-arm coder --whatineedtoknow
channel-rebind
channel-send sandbox/connect/feature-x/config planner coder IMPLEMENT body.md
rm /path/to/the/inbox/file
```

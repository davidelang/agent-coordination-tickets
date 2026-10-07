# Channel permissions v1

Version: v1
Date: 2026-10-06

How a human grants the channel scripts. The agents do not edit these grant files, do not `chown`, and do not use sudo. The forms below are what to add, checked against those products' docs on 2026-10-06.

The commands an agent runs are `channel-arm`, `channel-send`, and `rm` of the one inbox file `channel-arm` printed, after the work is done. That `rm` has no flags. `channel-setup` is yours, including `channel-setup -n`. Do not grant `mv`, `inotifywait`, or a blanket `python3`. A standing allow of `rm` is wider than one file: it also matches `rm -rf`. The prompt is what keeps the removal to that path.

Put [../bin](../bin/) or [../go](../go/) on the agent user's `PATH`, so the command text is `channel-arm` and not a long path. `bin/channel` is the shell script. `go/channel` is the static binary, and the three command names are symlinks to that binary. The agent runs the binary. If you would rather not change `PATH`, grant the absolute path the agent will actually type. Matchers compare the command text.

## Unix ownership

The script creates directories and sets modes. It does not change the owner. After `channel-setup`, you set the owner of each node directory to the Unix user who runs that agent. Both users belong to one shared group. Use mode bits. Do not use ACLs.

| Path | Mode | Owner | Why |
|---|---|---|---|
| node directory | `2750` | the agent whose inbox is inside it | That user writes the outgoing counter and the temp file. The group can traverse into the inbox and cannot replace the counter. |
| `inbox/` | `3775` | the receiving agent | Setgid so the message inherits the inbox group. Sticky so one sender cannot delete another's file. Group-writable so a sender in the group can create a file. The directory owner removes a file when the work is done. |
| message files | `0660` | the sender, group of the inbox | The script sets this mode. The receiver reads via the group. |
| `refs/` | `2775` | either sender, shared group | Every sender copies a reference here before the rename. |

Same Unix user on both sides still works. The modes are just tighter than a single-user directory needs.

`channel-send` renames from the sender's node directory into the recipient's inbox. Those two directories have to be on the same filesystem. A cross-device rename is not atomic, and the script stops rather than copying into the inbox.

## Write sandboxes

Some products confine writes to an allow-list. Reads stay on the Unix modes above. Each agent that sends or finishes needs a write grant that includes:

- its own node directory (temp file and the outgoing counter)
- its own inbox, so it can remove a finished file
- the inbox of every node it sends to
- the plan's `refs/` directory

Grant those directories. Do not widen a role to the whole machine to make one inbox writable.

## Grok Build

Project rules live in `.grok/config.toml` under `[permission]`. A pattern matches the command text as a prefix or as a whole-command glob, and a prefix has no word boundary (`channel-arm` also matches a longer command that merely starts with those letters). One rule per wrapper:

```
{ action = "allow", tool = "bash", pattern = "channel-arm*" },
{ action = "allow", tool = "bash", pattern = "channel-send*" },
{ action = "allow", tool = "bash", pattern = "rm *" },
```

The monitor tool runs that same command text, so these rules cover the watch. Do not add `allow = ["*"]`. An Always-allow clicked in the session is stored per OS user and per git worktree, under that user's `~/.grok`, and it dies as a way to share a grant with the other agent. Durable allows go in the project file. Rules load at session start.

`channel-setup` can stay off the agent allow list. You run it.

## Claude Code

Docs: https://code.claude.com/docs/en/permissions (page dated 2026-10-05). Rules are `Tool` or `Tool(specifier)`. For Bash, `Bash(channel-arm *)` is the word-boundary form. A trailing `:*` is documented as the same as ` *`. Deny wins over allow. Project rules go in `.claude/settings.json`. Personal project rules go in `.claude/settings.local.json`. User-wide rules go in `~/.claude/settings.json`. "Yes, and don't ask again" writes `.claude/settings.local.json` at the git root.

```
{
  "permissions": {
    "allow": [
      "Bash(channel-arm)",
      "Bash(channel-arm *)",
      "Bash(channel-send *)",
      "Bash(rm *)"
    ]
  }
}
```

Compound commands are matched per segment, so an allow for `channel-send` does not allow `channel-send ... && rm ...`. If the channel directory is outside the working directory, add it under `permissions.additionalDirectories`.

## Codex

Docs: https://developers.openai.com/codex/concepts/sandboxing (read 2026-10-06). Sandbox and approval are separate. The documented keys are `sandbox_mode`, `approval_policy`, `approvals_reviewer`, and `sandbox_workspace_write.writable_roots` in `~/.codex/config.toml`. That page does not document a per-command allowlist key, so this note does not invent one. Approve `channel-arm`, `channel-send`, and `rm` of the one FILE path when Codex asks, and decline `mv`, `inotifywait`, and a blanket `python3`.

`sandbox_mode = "workspace-write"` can write the workspace and temp directories. If the inboxes live outside the workspace, add that directory to `sandbox_workspace_write.writable_roots`. On Linux the sandbox is bubblewrap. The script needs to list the inbox and rename files into it. If bubblewrap hides `inotifywait`, `channel-arm` lists the directory instead and keeps running. Do not flip the session to `danger-full-access` to get an inotify watch.

## Cursor

Desktop docs: https://cursor.com/docs/reference/permissions (read 2026-10-06). `~/.cursor/permissions.json` and `<workspace>/.cursor/permissions.json` are concatenated. `terminalAllowlist` is a list of command prefixes. The docs' example is that `git` matches `git status` and does not match `gitk`. A file entry replaces the in-app terminal allowlist for that key. Run Mode has to be Allowlist or Auto-review (or Run Everything, which is wider than this channel needs). `autoRun` instructions are steering for Auto-review, not a guarantee.

```
{
  "terminalAllowlist": [
    "channel-arm",
    "channel-send",
    "rm"
  ]
}
```

The same page says the Cursor CLI permissions system is separate and does not document it there. When the CLI asks, approve those three names. This note does not guess the CLI file's keys.

## Antigravity, Gemini CLI, and other shell agents

Use whatever "approve this command" switch that product has, for `channel-arm`, `channel-send`, and `rm` of the one FILE path, and do not grant raw `mv` or `inotifywait`. The wake instructions are [shell-agent-v1.md](shell-agent-v1.md).

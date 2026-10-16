# Channel prompts, v1 (2026-10-06)

How to stand the channel up is [../README.md](../README.md). The filesystem sequence is the scripts in [../bin/](../bin/). A model that cannot run a shell cannot use the channel. Different model versions of the same product use the same wake paragraph.

`channel-arm` and a waiting `channel-send` use `inotifywait`. If it is missing or stops, they exit with an error; they never switch on their own. `CHANNEL_POLL=1`, set deliberately, makes them list the inbox every 0.2 seconds instead. That listing is inside the script, prints a line only when the oldest file changes, and uses no model tokens. The model still does not poll. This is part of v1.

The files below are sections of one prompt. Put the role section and the product paragraph in that one prompt. The agent does not open a second file to learn the messages. At session start, and on the first turn after a compact, it runs `channel-arm PLAN NODE --whatineedtoknow` and uses that output until the next compact. For Grok, that one sentence has to live in a project rule (`AGENTS.md` or `.grok/rules/*.md`), which is reloaded from disk.

## v2 (2026-10-15): no per-agent prompt for Grok Build and Cursor

The agent gets one instruction: run `channel-arm PLAN NODE --whatineedtoknow` and follow it (in an orchestration project this is the `/channel <diag> <node>` skill). That header is written for the agent tool named by `CHANNEL_AGENT` (`grok` or `cursor`, set by the launcher; unset or other values fail loudly with `CHANNEL-AGENT-UNKNOWN`) and holds what the role and product paragraphs below used to say: the watch command for that tool, duties per arrow from the diag, answering PING with PONG (the watch no longer does), replying, keeping current work, fresh-session arming, no push/deploy from a message, BLOCKED handling, and backgrounding sends and reading their delivery outcome (DELIVERED / RETRACTED / ABORTED, also in `nodes/NODE/outcome/`), and `channel-send --retract` to take a message back. `grok-v1`, `cursor-v1`, `planner-v1` and `coder-v1` are replaced by it. The Claude, Codex and shell-agent paragraphs are still v1, but `--whatineedtoknow` refuses those tools until they get a `CHANNEL_AGENT` value of their own.

## v1

| File | Version | Date | Section |
|---|---|---|---|
| [planner-v1.md](planner-v1.md) | v1 | 2026-10-06 | Planner role. Any product. |
| [coder-v1.md](coder-v1.md) | v1 | 2026-10-06 | Coder role. Any product. |
| [grok-v1.md](grok-v1.md) | v1 | 2026-10-06 | Grok Build when the session has the monitor tool. Grok 4.5 and Grok 4.6 use this same paragraph. |
| [claude-v1.md](claude-v1.md) | v1 | 2026-10-06 | Claude Code. |
| [codex-v1.md](codex-v1.md) | v1 | 2026-10-06 | Codex CLI and the Codex IDE extension. |
| [cursor-v1.md](cursor-v1.md) | v1 | 2026-10-06 | Cursor desktop agent. |
| [shell-agent-v1.md](shell-agent-v1.md) | v1 | 2026-10-06 | Antigravity, Gemini CLI, a Grok session with no monitor tool, and any other agent that has a shell. |
| [permissions.md](permissions.md) | v1 | 2026-10-06 | Human note: Unix ownership, and how each product is granted the wrappers. |

Bump a product paragraph when only that product's wake or allow syntax changes. Bump `planner-v1` and `coder-v1` together when the verbs or the message body change. Leave the old file in place with a one-line "replaced by" note so a running agent can be pointed at a named version.

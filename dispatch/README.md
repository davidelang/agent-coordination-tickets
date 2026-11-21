# dispatch

`dispatch/dispatch` is a headless channel node. It takes the place of a long-running coder session. For each message it starts **one fresh agent** through a launch hook, checks the result, runs the blocking reviews, and replies on the channel. It never prompts.

```
dispatch -d DIAG -r executor|reviewer|tester -l HOOK            # watch the node's inbox
dispatch -d DIAG -r executor -l HOOK --once                      # handle the oldest message, exit
dispatch -d DIAG -r executor -l HOOK -p PLAN [-f STEP]           # one job, no channel
dispatch -d DIAG -r reviewer -l HOOK -p PLAN -R A..B             # one review, no channel
dispatch ... -n                                                  # dry run: settings + first launch
```

`dispatch -h` lists every option. All options have a short and a long form (GNU `getopt`, util-linux).

| Path | What |
|--|--|
| `dispatch` | the dispatcher (bash) |
| `lib/common.sh` | config, diag, message and template helpers |
| `backends/grok-build.sh` | Grok Build headless (`--prompt-file --rules --output-format json`), tested |
| `backends/cursor.sh`, `backends/antigravity.sh` | built from `--help`, untested |
| `backends/grokbot-webhook.sh` | stub, refuses to run |
| `roles/ROLE.conf` | per-role defaults (access, turns, reply verbs, review registry) |
| `roles/ROLE.md` | role card, always passed as the agent's rules |
| `roles/*.prompt` | job prompt templates (`{{PLAN}}`, `{{RANGE}}`, `{{VERDICT}}`, ...) |
| `hooks/direct` | trivial launch hook: runs the agent as the current user |

The dispatcher knows nothing about accounts, landlock, cwd beyond the git worktree, config homes, or a project's `roles.config`. Those belong to the launch hook, a consumer's `run-*` wrapper. Full description: [docs/dispatch.md](../docs/dispatch.md). Example settings: [example/comms.dispatch](../example/comms.dispatch).

# Dispatcher

`dispatch/dispatch` serves one node of a channel plan without a long-lived agent session. It reads messages from the node's inbox, starts one fresh headless agent per job through a **launch hook**, decides from the agent's output and the git state whether the job is done, runs the reviews, and replies with `channel-send`. It never prompts: a missing diag, role or hook is an error (exit 2).

The split of work:

| Here (generic) | In the consumer's `run-*` wrapper (the launch hook) |
|--|--|
| the job loop, review rounds, done checks, replies | which account runs each role (`sudo -u ...`) |
| the agent command line for each backend | the landlock for each role and access level |
| role cards and prompt templates (overridable) | the config home (`GROK_HOME`), which sets the context window |
| reading the diag and the sidecar | finding the standard diag and asking whether to use it |
| | effort and model from the project's `roles.config`, passed as `-e`/`-m` |

## Command line

```
dispatch -d DIAG -r ROLE [-l HOOK] [options]
```

| Option | Meaning |
|--|--|
| `-d, --diag FILE` | channel plan. Required; there is no default (the wrapper finds `sandbox/connect/<branch>/config`) |
| `-r, --role ROLE` | `executor`, `reviewer` or `tester`. Required |
| `-l, --launch CMD` | launch hook. Required here or as `launch=` / `ROLE.launch=` |
| `-p, --plan FILE` | one job, no channel. Executor: implement; reviewer/tester: review `-R` |
| `-f, --from-step STEP` | executor start step (default: the beginning) |
| `-R, --range A..B` | commit range for a direct reviewer/tester job |
| `-1, --once` | handle the oldest inbox message, then exit |
| `-N, --node NAME` | node this process serves (default `ROLE.node`) |
| `-C, --worktree DIR` | git worktree the agent works in (default: toplevel of `$PWD`) |
| `-b, --backend NAME` | `grok-build` (default), `cursor`, `antigravity`, `grokbot-webhook` |
| `-m, --model ID` / `-e, --effort LEVEL` | passed to the backend |
| `-t, --max-turns N` | turn cap per agent run (default 80 executor, 40 reviewer, 60 tester) |
| `-T, --timeout SEC` | wall-clock cap per run, via `timeout` (0 = none) |
| `-u, --rules FILE` | role card for this role |
| `-k, --rounds N` | review rounds before FAILURE (default 3) |
| `-w, --review-launch CMD` | hook for the reviews an executor runs |
| `-s, --sidecar FILE` | settings file (default `DIAG.dispatch` if present) |
| `-c, --channel PATH` | channel program (default `channel` on `PATH`) |
| `-L, --log-dir DIR` | job logs (default `DIAGDIR/dispatch/NODE`) |
| `-n, --dry-run` | print the settings and the agent command lines; start nothing, send nothing |
| `-v, --verbose` | also copy agent output to stdout |
| `-h, --help` | help |

Exit status: 0 done / PASS, 1 not done / FAIL, 2 usage or setup error, 3 a direct review could not produce a verdict.

Run it with its output logged, for example `dispatch ... 2>&1 | tee "$T/dispatch.log"`. Each agent run also leaves its own files (below).

## The launch hook

The dispatcher builds the agent's command line and runs it through the hook:

```
HOOK --role ROLE --access read-write|read-only --job-dir DIR --worktree DIR -- AGENT-ARGV...
```

with `DISPATCH_ROLE`, `DISPATCH_ACCESS`, `DISPATCH_JOB_DIR` and `DISPATCH_WORKTREE` also in the environment. The hook picks the account, landlock and environment for that role and access, then runs `AGENT-ARGV` with stdin closed. Its stdout must be the agent's stdout (the backend parses it); its exit status is passed through. The hook string is split on spaces, so extra fixed arguments may follow the program (`-l "run-script-grok-hook --project wifi"`).

`dispatch/hooks/direct` is the trivial hook: it runs the agent as the current user in the worktree, with no landlock. It is for tests and is the template for a real hook.

Reviewers and testers get `--access read-only`. The hook is what enforces it (landlock). The dispatcher also checks: if HEAD or `git status` changed during a read-only run, the review counts as ERROR.

### Context window

Grok Build has no command-line option for the context window, and the `GROK_CONFIG` overlay ignores `context_window`. What works headless (tested with grok 1.0.92, session recorded `contextWindowTokens: 500000`) is a separate config home: a directory whose `config.toml` has

```toml
[model."grok-4.7"]
context_window = 500000
```

with every other entry symlinked back to the account's real `~/.grok`, so sign-in and sessions stay where they are. The hook sets `GROK_HOME=that-directory` for roles that should get 500K. The dispatcher does not do this; it is per account and per project.

## Settings

Settings come from, highest first:

1. the command line (applies only to the role this process was started as),
2. the sidecar (`DIAG.dispatch`, or `-s FILE`), `KEY=VALUE` lines,
3. `# dispatch.KEY=VALUE` lines in the diag (the channel skips every `#` line, so its parser is unchanged),
4. `dispatch/roles/ROLE.conf`.

Inside each source `review.NAME.KEY` beats `ROLE.KEY`, which beats a bare `KEY`. Relative paths (`rules`, `prompt`, `fix_prompt`, `evidence_prompt`, `addendum`, and the program in `launch`, `channel`, `agent_bin`) are resolved from the file that set them. [example/comms.dispatch](../example/comms.dispatch) shows every key.

| Key | Default | Meaning |
|--|--|--|
| `ROLE.node` | none | node served in watch / `--once` mode |
| `launch`, `ROLE.launch` | none | launch hook |
| `channel` | `channel` | channel program |
| `backend` | `grok-build` | backend |
| `agent_bin` | `grok` / `cursor-agent` / `agy` | agent program the backend runs |
| `model`, `effort` | none | passed to the backend when set |
| `max_turns` | 80 / 40 / 60 | turn cap |
| `timeout` | 0 | seconds per run |
| `rules` | `roles/ROLE.md` | role card (always passed: `--rules` for grok, prepended to the prompt otherwise) |
| `prompt`, `fix_prompt`, `evidence_prompt` | `roles/*.prompt` | templates |
| `addendum` | none | file appended to the prompt as `{{ADDENDUM}}` (project steps such as a preflight) |
| `executor.rounds` | 3 | review rounds before FAILURE |
| `executor.require_commit` | yes | SUCCESS without a new commit is not done |
| `executor.done_tag` | none | tag that must point at HEAD (e.g. `builds`) |
| `ROLE.reply.KIND` | executor: COMPLETE, FAILURE, BLOCKED, STOPPED; reviewer/tester: none | reply verb per outcome (`complete`, `failure`, `blocked`, `stopped`; `pass`, `fail`, `error`) |
| `review.NAME.role` | `reviewer` | role that runs the review |
| `review.NAME.enabled` | yes | |
| `review.NAME.blocking` | yes | blocking reviews gate the reply; non-blocking start after COMPLETE |
| `review.NAME.mode` | `in-process` | `channel`: send `verb` to `node` and do not wait (non-blocking only in v1) |
| `review.NAME.on_fail_verb` | none | non-blocking in-process review: verb sent to the planner on FAIL |

Account, landlock, cwd and config-home keys do not exist here on purpose.

## What a job does

### Executor (`IMPLEMENT` / `RESUME`, or `-p PLAN`)

1. Move the message to `inbox/claimed/`, record HEAD.
2. Round 1: render `executor.prompt` (plan, start step, recorded HEAD, addendum) and start a fresh agent.
3. Done check. Not done when: there is no `STATUS:` line; `STATUS:` is not SUCCESS (BLOCKED and NEEDS-INPUT reply BLOCKED, anything else FAILURE); HEAD did not move (`require_commit`); `done_tag` is set and not on HEAD; the run timed out.
4. Each enabled blocking review runs as a fresh read-only agent on `RECORDED..HEAD`. Its last `STATUS:` line is the verdict (PASS / FAIL; anything else, no line, or a changed tree is ERROR → FAILURE). The full reply is saved as `verdict.txt`.
5. On FAIL, a new round: a fresh executor gets `executor-fix.prompt` with the plan, the range so far and the verdict text, and must verify each finding against the code before fixing it, and say which it rejects. Then the reviews run again on the whole range. After `rounds` rounds still failing, the reply is FAILURE with the verdict path.
6. Reply (`COMPLETE` / `FAILURE` / `BLOCKED`) to the sender, with the body built from the diag's message block (`STATUS`, `PLAN`, `RESPONDING-TO`, `REASON` filled; other `<...>` fields get `none`). The claimed file is removed only after the reply is sent.
7. After COMPLETE, non-blocking reviews start in the background.

Sessions are never resumed: a resumed session carries its whole history and compacts again, which costs more than a fresh session given the plan, the range and the verdict.

`PROVIDE-EVIDENCE` runs `executor-evidence.prompt` (run the CHECK, change nothing) and replies COMPLETE or FAILURE. `STOP` replies STOPPED: jobs run one at a time, so a STOP is read between jobs (an agent already running stops at its turn cap or timeout). `PING` is answered with `PONG`.

### Reviewer / tester node (or `-p PLAN -R A..B`)

A message with `PLAN:` and `RANGE:` gets one read-only review. The reply verb is `ROLE.reply.pass|fail|error` (none by default); the body's `STATUS`, `REASON`, `VERDICT` / `VERDICT-FILE` fields are filled.

### Restart

On start the dispatcher lists anything left in `inbox/claimed/` and does not redo it. Someone decides; `channel-status` shows the ages.

## Files per job

`LOG_DIR/YYYYmmdd-HHMMSS-ACTION/`: `message.txt`, `job.txt` (plan, recorded HEAD), one directory per run (`r1-executor/`, `r1-review-conformance/`, `r2-executor/`, ...) holding `prompt.txt`, `argv.txt` (exact command, reproducible), `agent.out` (the `--output-format json` result), `agent.err`, `result.txt` (final text), and `verdict.txt` for reviews; `reply.txt`; `nonblocking.log`. `LOG_DIR/costs.tsv` gets one line per run: time, role, exit, cost in USD.

## Backends

A backend is `dispatch/backends/NAME.sh` defining `backend_argv` (fills `BACKEND_ARGV` from `B_PROMPT_FILE`, `B_RULES_FILE`, `B_WORKTREE`, `B_MODEL`, `B_EFFORT`, `B_MAX_TURNS`, `B_ACCESS`, `B_AGENT_BIN`), `backend_text FILE` (final text from the agent's stdout) and `backend_cost FILE`.

- `grok-build`: `grok --prompt-file P --rules CARD --output-format json --cwd WT --always-approve --permission-mode auto --no-subagents --no-alt-screen --max-turns N [--model M] [--reasoning-effort E]`. Tested with grok 1.0.92.
- `cursor`, `antigravity`: built from `--help`, untested.
- `grokbot-webhook`: stub. Waking a Grok Bot node is already possible with the channel's per-node `webhook=`.

## Shipping it through a pin

A consumer that pins this repository today ships only `artifact/channel` and its links. To ship the dispatcher, the consumer's `libpin.toml` needs one `[[artifact]]` entry per file below (copied from the source checkout, no build step), and its `SOURCE.md` should say that agents and wrappers run `artifact/dispatch/dispatch`:

```
dispatch/dispatch
dispatch/lib/common.sh
dispatch/backends/grok-build.sh
dispatch/backends/cursor.sh
dispatch/backends/antigravity.sh
dispatch/backends/grokbot-webhook.sh
dispatch/hooks/direct
dispatch/roles/executor.conf   dispatch/roles/reviewer.conf   dispatch/roles/tester.conf
dispatch/roles/executor.md     dispatch/roles/reviewer.md     dispatch/roles/tester.md
dispatch/roles/executor.prompt dispatch/roles/executor-fix.prompt dispatch/roles/executor-evidence.prompt
dispatch/roles/reviewer.prompt dispatch/roles/tester.prompt
```

The files must keep this layout relative to each other (`dispatch` finds the rest from its own location, following symlinks). Needs bash 4.3+, GNU `getopt`, `jq`, `git`, coreutils `timeout`.

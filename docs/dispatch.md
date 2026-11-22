# Dispatcher

`dispatch/dispatch` serves one node of a channel plan without a long-lived agent session. It reads messages from the node's inbox, starts one fresh headless agent per job through a **launch hook**, decides from the agent's output and the git state whether the job is done, runs the reviews, and replies with `channel-send`. It never prompts: a missing diag, role or hook is an error (exit 2).

The split of work:

| Here (generic) | In the consumer's `run-*` wrapper (the launch hook) |
|--|--|
| the job loop, review rounds, done checks, replies | which account runs the agents (the wrapper may switch once at start with `sudo -u`, so the dispatcher and every agent share one account; or the hook may pick one per role) |
| the agent command line for each backend | the landlock for each role and access level |
| generic role cards (a worktree's own `roles/<role>.md` wins) and the one-line job prompts | the config home (`GROK_HOME`), which sets the context window |
| reading the diag: nodes, the REVIEW arrow, reply verbs | finding the standard diag and asking whether to use it |
| | effort and model from the project's `roles.config`, passed as `-e`/`-m` |
| | the project's options: node, role cards (from the worktree), done tag, retry effort, failure log dir |

## Command line

```
dispatch -d DIAG -r ROLE [-l HOOK] [options]
```

| Option | Meaning |
|--|--|
| `-d, --diag FILE` | channel plan. Required; there is no default (the wrapper finds `sandbox/connect/<branch>/config.diag`) |
| `-r, --role ROLE` | `executor`, `reviewer` or `tester`. Required |
| `-l, --launch CMD` | launch hook. Required |
| `-p, --plan FILE` | one job, no channel. Executor: implement; reviewer/tester: review `-R` |
| `-f, --from-step STEP` | executor start step (default: the beginning) |
| `-R, --range A..B` | commit range for a direct reviewer/tester job |
| `-1, --once` | handle the oldest inbox message, then exit |
| `-N, --node NAME` | node this process serves (required to watch) |
| `-C, --worktree DIR` | git worktree the agent works in (default: toplevel of `$PWD`) |
| `-b, --backend NAME` | `grok-build` (default), `cursor`, `antigravity`, `grokbot-webhook` |
| `-m, --model ID` / `-e, --effort LEVEL` | passed to the backend |
| `-t, --max-turns N` | turn cap per agent run (default 200 executor, 80 reviewer, 80 tester) |
| `-T, --timeout SEC` | wall-clock cap per run, via `timeout` (0 = none) |
| `-u, --rules FILE` | role card for this role (default: `roles/ROLE.md` at the worktree root, else `dispatch/roles/ROLE.md`) |
| `--agent-bin PATH` | agent program the backend runs (default `grok` / `cursor-agent` / `agy`) |
| `-w, --review-launch CMD` | hook for in-process reviews (default: the same hook) |
| `--done-tag TAG` | executor: a tag that must be on HEAD for the job to be done (e.g. `builds`) |
| `--retry-effort LEVEL` | executor: effort for the one retry after a session that ended not done |
| `--failure-log-dir DIR` | executor: also copy `review-history.md` here when a job goes back |
| `--review-timeout SEC` | executor: wait for a channel review verdict (default 3600) |
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

with `DISPATCH_ROLE`, `DISPATCH_ACCESS`, `DISPATCH_JOB_DIR` and `DISPATCH_WORKTREE` also in the environment. The hook picks the account, landlock and environment for that role and access, then runs `AGENT-ARGV` with stdin closed. Its stdout must be the agent's stdout (the backend parses it); its exit status is passed through. When the hook refuses before starting the agent (program missing, bad config) it must print the reason on stderr, nothing on stdout, and exit 127; the dispatcher reports that as BLOCKED with the reason and does not retry (exit 126/127 with empty stdout is also what `exec` gives for a missing or non-executable program). The hook string is split on spaces, so extra fixed arguments may follow the program (`-l "run-script-grok-hook --project wifi"`).

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

There is no settings file. Each thing lives where it belongs:

- **Parameters of the run** are options: the ones above, given by the consumer's `run-*` wrapper (which takes project values such as the sandbox or `roles.config` efforts from the worktree it runs in). The role is fixed by the wrapper (`-r`), as `run-grok-coder` fixes `ROLE_KEY`, and the per-role defaults are cases in the dispatcher itself (`role_default` in `lib/common.sh`: access read-write / read-only, `max_turns` 200 / 80 / 80 (from live runs: one plan took 187 executor turns over three sessions, a review up to 60), `timeout` 0, the prompt templates, the commit check, the one `conformance` review); there is no per-role settings file. The command line beats them.
- **Rules for the agent** are role cards, found the way `run-grok-<role>` finds its own (`roles/<role>.md` at the worktree root, passed as `--rules`): `roles/executor.md`, `roles/reviewer.md` (also for an in-process review), `roles/tester.md`. A worktree without one gets the generic `dispatch/roles/ROLE.md`; `-u` overrides this process's own card. They say: what done means, committing, never resetting past the done tag, the reviewer's numbered findings and `PROBLEMS: N`.
- **Messages** come from the diag: the node's inbox (`node`), whether reviews go over the channel (a `verb THIS-NODE OTHER REVIEW` line makes OTHER the review node), and the reply verbs. The executor replies `COMPLETE`, `FAILURE`, `BLOCKED` or `STOPPED`; a reviewer or tester replies `REQUEST-PASS` / `-FAIL` / `-ERROR` (`REVIEW-PASS` ...). A reply is sent only when the diag lists that verb from this node to the sender; otherwise the message stays claimed with a warning.

A `DIAG.dispatch` file from an earlier version is ignored with a warning. Account, landlock, cwd and config-home choices do not exist here on purpose.

## What a job does

### Executor (`IMPLEMENT` / `RESUME`, or `-p PLAN`)

1. Move the message to `inbox/claimed/`, record HEAD.
2. Round 1: start a fresh agent with the role card as its rules and a one-line prompt: `Read the rules. Implement PLAN [from step N].`
3. Done check. Not done when: there is no `STATUS:` line; `STATUS:` is not SUCCESS (BLOCKED and NEEDS-INPUT reply BLOCKED, anything else FAILURE); HEAD did not move (`require_commit`); `--done-tag` is given and not on HEAD; the run timed out.
4. Each enabled blocking review checks `RECORDED..HEAD`. In process, it is a fresh read-only agent; its last `STATUS:` line is the verdict (PASS / FAIL; anything else, no line, or a changed tree is ERROR). Over the channel (the diag has a `REVIEW` arrow from this node), the executor sends `REVIEW` (PLAN, RANGE, ROUND, REF) to the reviewer node and waits in its own inbox for the reply with the same `REF`, so the planner is not woken for reviews. While waiting, other messages stay queued, a `STOP` ends the job (reply STOPPED, the STOP is consumed), and after `--review-timeout` the request is retracted and the review counts as no verdict. A reply that arrives after its wait has ended is removed as stale. A review with no verdict is retried once as a fresh run (roles/coder.md); a second ERROR is FAILURE (a tool problem; no cycle is used). A FAIL must carry a `PROBLEMS: N` line (numbered findings, then the count); a FAIL without one counts as no verdict. The full reply is saved as `verdict.txt`.
5. Review cycles: at most 3 per job (fixed).
   - After a FAIL the full review is saved as `review-N.md` and appended to `review-history.md` in the job directory. A fresh session is told `Read the rules. Fix the issues in JOB/review-N.md (review N of 3 of RANGE against PLAN).`; the role card says how (verify each finding, say which it rejects, commit fixes on top; earlier reviews are the `review-*.md` beside it). Then the range is reviewed again.
   - If a review reports as many problems as the review before it or more, or the third review still fails, the job ends: FAILURE "re-plan needed" to the sender, naming `review-history.md` (copied to `--failure-log-dir` when given). BLOCKED is never used for this: it is only for something outside the code that prevents progress: the agent's own `STATUS: BLOCKED` (permissions, contradictory instructions, missing access), or an agent that never started (below).
   - A session that ends not done (FAILURE, no STATUS, no commit, `done_tag` not on HEAD, timeout) gets one fresh retry per job (`Read the rules. Finish PLAN [from step N]. The previous session stopped without finishing; its last reply is FILE.`) at `--retry-effort`; a second one is FAILURE. It does not use a review cycle.
   - Launch failure: when the launch hook exits 126 or 127 with nothing on stdout, the agent never started (program missing or not executable, the hook refused: bad config, no sandbox). The job ends at once with BLOCKED to the sender, the reason being the last lines of the hook's stderr (`agent.err`); there is no retry and no higher effort, since a rerun fails the same way. A review agent that never starts is the same: in process the job ends BLOCKED; a reviewer node replies `REVIEW-ERROR` with `STATUS: BLOCKED`, which the waiting executor passes on as BLOCKED without a second review. A launch hook must keep to this: refuse before the agent starts with a reason on stderr and exit 127.
   - The dispatcher never resets, reverts or drops commits: whatever the job committed is left in place for the planner. The role card tells each session never to reset or check out past the `builds` tag (the done tag). Build slips (a missed reference, a typo) are the agent's to fix inside its own session; the dispatcher counts no strikes.
6. Reply (`COMPLETE` / `FAILURE` / `BLOCKED` / `STOPPED`, as the diag allows) to the sender, with the body built from the diag's message block (`STATUS`, `PLAN`, `RESPONDING-TO`, `REASON` filled; other `<...>` fields get `none`). The claimed file is removed only after the reply is sent.

Prompts carry only the job (plan, range, step, review file); everything else an agent must follow (the STATUS line, committing, the done tag, no reset past `builds`, numbered findings and `PROBLEMS: N`) is in the role card passed as rules. Sessions are never resumed: a resumed session carries its whole history and compacts again, which costs more than a fresh session given the plan, the range and the verdict.

`PROVIDE-EVIDENCE` runs `Read the rules. Rerun this check for PLAN and report the result, changing nothing: CHECK` and replies COMPLETE or FAILURE. `STOP` replies STOPPED: jobs run one at a time, so a STOP is read between jobs or while a channel review is awaited (an agent already running stops at its turn cap or timeout). `PING` is answered with `PONG`.

### Reviewer / tester node (or `-p PLAN -R A..B`)

A message with `PLAN:` and `RANGE:` gets one read-only review. The reply goes to whichever node sent the request; its verb is the request verb plus `-PASS` / `-FAIL` / `-ERROR` (`REVIEW-PASS` ...), sent only when the diag allows it; the body's `STATUS`, `REASON`, `VERDICT` / `VERDICT-FILE` fields are filled, `REF` / `ROUND` are copied from the request, and `PROBLEMS` is the count from the review.

### Restart

On start the dispatcher lists anything left in `inbox/claimed/` and does not redo it. Someone decides; `channel-status` shows the ages.

## Files per job

`LOG_DIR/YYYYmmdd-HHMMSS-ACTION/`: `message.txt`, `job.txt` (plan, recorded HEAD), one directory per run (`r1-executor/`, `r1-review-conformance/`, `r2-executor/`, ...) holding `prompt.txt`, `argv.txt` (exact command, reproducible), `agent.out` (the `--output-format json` result), `agent.err`, `result.txt` (final text), and `verdict.txt` for reviews; `reply.txt`; for failed reviews `review-N.md` and `review-history.md`. `LOG_DIR/costs.tsv` gets one line per run: time, role, exit, cost in USD.

## Backends

A backend is `dispatch/backends/NAME.sh` defining `backend_argv` (fills `BACKEND_ARGV` from `B_PROMPT_FILE`, `B_RULES_FILE`, `B_WORKTREE`, `B_MODEL`, `B_EFFORT`, `B_MAX_TURNS`, `B_ACCESS`, `B_AGENT_BIN`), `backend_text FILE` (final text from the agent's stdout) and `backend_cost FILE`.

- `grok-build`: `grok --prompt-file P --rules CARD --output-format json --cwd WT --always-approve --permission-mode auto --no-subagents --no-alt-screen --max-turns N [--model M] [--reasoning-effort E]`. Tested with grok 1.0.92.
- `cursor`: built from `--help`, untested.
- `antigravity`: `agy --print --dangerously-skip-permissions [--effort E] [--model M] "CARD\n\nPROMPT"`, plain-text reply, no turn cap, no cost. Built from `--help`, untested. agy reads flags only before the prompt, so a launch hook that adds flags inserts them after the program name.
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
dispatch/roles/executor.md     dispatch/roles/reviewer.md     dispatch/roles/tester.md
```

The files must keep this layout relative to each other (`dispatch` finds the rest from its own location, following symlinks). Needs bash 4.3+, GNU `getopt`, `jq`, `git`, coreutils `timeout`.

## Test

`tests/dispatch` runs two real dispatcher processes (coder and reviewer) with a scripted fake agent on a scratch diag: review FAIL, fix, PASS over the channel; a message queued during the wait; STOP during the wait; a stale reply; review timeout; a session that ends not done (once, twice); review cycles with falling problems, rising problems, and three FAILs; no reset past the builds tag. `tests/run` calls it. No real agent runs.

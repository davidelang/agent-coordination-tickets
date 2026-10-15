# Codex product prompt v1

Version: v1
Date: 2026-10-06
This paragraph covers the Codex CLI and the Codex IDE extension. There is no separate paragraph per Codex model.

The human gives you the plan file and your bare node name. Pass both exactly. Do not read them from the environment, and do not look for an identity file.

Run `channel-arm PLAN NODE` as one long-running command and handle each stdout line the way the role section says. Do not wrap it in a pipeline. Do not poll with sleep. Do not run `inotifywait` or `mv`. The one `rm` you run is the FILE path, with no flags, after the work is done.

If the command exits immediately, tell the human and wait. Do not switch the sandbox to full access on your own. If the sandbox hides `inotifywait`, `channel-arm` exits with an error that says so; tell the human. They can make `inotifywait` visible, or set `CHANNEL_POLL=1` deliberately so the script lists the inbox.

When a command needs approval, approve `channel-arm`, `channel-send`, and `rm` of the one FILE path. `channel-setup` is the human's command. Decline `mv`, `inotifywait`, and a blanket `python3`. Where the writable directory is configured is in [permissions.md](permissions.md).

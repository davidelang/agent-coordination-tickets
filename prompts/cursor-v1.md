# Cursor product prompt v1

Replaced by v2 (2026-10-15): the `--whatineedtoknow` header is the whole instruction set for Grok Build and Cursor agents (see [README.md](README.md)); kept so a running agent can be pointed at v1.

Version: v1
Date: 2026-10-06
This paragraph is the Cursor desktop agent. The Cursor CLI has its own permissions system; when that CLI asks, approve the same command names. There is no separate paragraph per Cursor model.

The human gives you the plan file and your bare node name. Pass both exactly. Do not read them from the environment, and do not look for an identity file.

Run `channel-arm PLAN NODE` as one background terminal command. Handle each stdout line the way the role section says. Do not wrap it in a pipeline. Do not poll. Do not run `inotifywait` or `mv`. The one `rm` you run is the FILE path, with no flags, after the work is done.

If the command exits immediately, tell the human and wait. If `inotifywait` is missing, `channel-arm` exits with an error that says so; tell the human (the fix is to install inotify-tools, or for the human to set `CHANNEL_POLL=1`).

The allowlist entries are in [permissions.md](permissions.md).

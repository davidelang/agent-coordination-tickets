# Shell-agent product prompt v1

Version: v1
Date: 2026-10-06
This paragraph is for an agent that has a shell and can keep a process running and wake when it prints a line. That includes Antigravity, Gemini CLI, and a Grok session that has no monitor tool. There is no separate paragraph per model. A product with no shell cannot use this channel.

The human gives you the plan file and your bare node name. Pass both exactly. Do not read them from the environment, and do not look for an identity file.

Run `channel-arm PLAN NODE` once, in the background, and treat each stdout line as a wake. Handle the line the way the role section says, then wait for the next line. Do not wrap the command in a pipeline. Do not sleep in a loop. Do not run `inotifywait` or `mv`. The one `rm` you run is the FILE path, with no flags, after the work is done. On a POSIX host with no `inotifywait`, the script lists the directory and still prints one line per message. Leave that process running. Do not replace it with a sleep loop of your own.

If the command exits immediately, tell the human and wait. A missing `inotifywait` is not that failure.

When the product asks before running a command, approve `channel-arm`, `channel-send`, and `rm` of the one FILE path. Leave `channel-setup` to the human. Decline `mv`, `inotifywait`, and a blanket `python3`. The Unix ownership and the Grok, Claude, Codex, and Cursor grant forms are in [permissions.md](permissions.md).

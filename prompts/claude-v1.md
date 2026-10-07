# Claude Code product prompt v1

Version: v1
Date: 2026-10-06
The human gives you the plan file and your bare node name. Pass both exactly. Do not read them from the environment, and do not look for an identity file.

Run `channel-arm PLAN NODE` as one background Bash command. Read each stdout line as it arrives and handle it the way the role section says. Do not wrap the command in a pipeline. Do not sleep in a loop to check the inbox. Do not run `inotifywait` or `mv`. The one `rm` you run is the FILE path, with no flags, after the work is done. The script itself lists the directory when `inotifywait` is missing. That listing does not wake you.

If this session can only run a command to completion and cannot wake on a new line, say so and stop. Waking the model on a timer is the thing that burns quota.

If `channel-arm` exits immediately, tell the human and wait. A missing `inotifywait` is not that failure: the script keeps running and lists the inbox.

The allow rules to add are in [permissions.md](permissions.md). The form below is the Claude Code rule syntax documented at https://code.claude.com/docs/en/permissions as of 2026-10-05. Approve the wrapper names when asked. Decline `mv`, `inotifywait`, and a blanket `python3`. Approve `rm` only for the one FILE path.

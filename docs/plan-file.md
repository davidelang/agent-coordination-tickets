# Plan file

One blockdiag file. The picture comes first. The key is below it, inside a `/* */` comment, because blockdiag drops that comment and then requires the rest of the file to be empty. The script reads the lines inside the comment. `/* */` does not hide them.

A line that starts with `#` or `//` is a note. The script skips it. Put `#` or `//` in front of a `node`, `verb`, or `message` line to leave that line out.

`node`, `verb`, and `message` may appear in any order. A verb word that has no `message` block is an error. A `message` block that no verb line uses is allowed. Two directions may name the same word. They share one block.

## node

```
node NAME /absolute/inbox
```

Messages to NAME are placed in that directory by an atomic rename. When NAME finishes a message, it removes the file. If NAME is restarted, it handles the oldest file still in the inbox. The path is absolute.

A node line may also carry a webhook, fired after each message queued to NAME:

```
node NAME /absolute/inbox webhook=URL [webhook_key=TOKEN | webhook_key_file=PATH] [webhook_retries=N] [webhook_backoff_max=T]
```

Give only the key; the tool sends `Authorization: Bearer <key>` (a leading `Bearer ` or `Authorization: Bearer ` is stripped). The inline key and the key file are both fully supported. A relative `webhook_key_file` is resolved from the plan file's directory, and the file's first line is the key. The POST body is `{"target","from","verb","inbox","file"}` as JSON. A failed POST is retried in a detached background process with exponential backoff (defaults: `webhook_retries=12`, `webhook_backoff_max=30m`; the max accepts `N`, `Ns`, `Nm`, `Nh`), so the send returns after the first try. A failed first try prints a `WARNING`; the message stays queued, the send still succeeds, and the background outcome goes to syslog. Details: [bin/README.md](../bin/README.md#webhooks-optional).

## Built-in PING and PONG

PING and PONG are allowed on every arrow without a verb line or message block (body `NONCE: <value>`). `channel-ping` uses them and `channel-arm` answers them; a PING with `SCOPE: all` and `RUN: <id>` lines is a check-all (`channel-ping --all`) and the receiver passes it on. If the plan defines PING or PONG blocks, the bodies the tools build follow them (literal lines copied; only `NONCE`, `SCOPE`, `RUN` placeholders can be filled, anything else is a `CHANNEL-CONTROL-BODY` error). A PONG that `channel-arm` cannot send is reported as `CHANNEL-AUTO-REPLY-FAILED` and the PING is shown as an ordinary `FILE`. A plan may still spell them out; the commented block in `example/comms.diag` shows the shape.

## verb

```
verb SOURCE DEST WORD...
```

SOURCE may send those words to DEST. The diagram has to draw that direction. A label on the arrow is not read.

## message

```
message IMPLEMENT {
This message is one approved plan to be implemented.
body: {
ACTION: IMPLEMENT
PLAN: <path/to/plan>
FROM-STEP: <step, or the beginning>
}
}
```

The text inside the outer braces is what the agent learns. `channel-arm FILE NODE --whatineedtoknow` prints a "how to act" header (paths, the exact watch and send commands, body rules, remove-when-done), then the blocks on the arrows that touch that node, and exits. The same flag works on `channel-setup` and `channel-send`. `body:` is the shape of the file passed to `channel-send`. A field in `<...>` is filled in. Every other line is sent unchanged. `channel-send` rejects a body that drops a verbatim line or leaves a `<...>` field unfilled.

## Arrows

Above the diagram, three lines are enough:

```
# -> waits. One unremoved file from that sender, then the next send waits.
# -< does not wait. Files queue at the crow's foot, and that foot can be at either end.
# <-> is -> in both directions.
```

The background for those operators is [diagram-marks.md](diagram-marks.md).

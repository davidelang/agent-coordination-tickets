# Plan file

One blockdiag file. The picture comes first. The key is below it, inside a `/* */` comment, because blockdiag drops that comment and then requires the rest of the file to be empty. The script reads the lines inside the comment. `/* */` does not hide them.

A line that starts with `#` or `//` is a note. The script skips it. Put `#` or `//` in front of a `node`, `verb`, or `message` line to leave that line out.

`node`, `verb`, and `message` may appear in any order. A verb word that has no `message` block is an error. A `message` block that no verb line uses is allowed. Two directions may name the same word. They share one block.

## node

```
node NAME /absolute/inbox
```

Messages to NAME are placed in that directory by an atomic rename. When NAME finishes a message, it removes the file. If NAME is restarted, it handles the oldest file still in the inbox. The path is absolute.

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

The text inside the outer braces is what the agent learns. `channel-arm FILE NODE --whatineedtoknow` prints the blocks on the arrows that touch that node, then exits. The same flag works on `channel-setup` and `channel-send`. `body:` is the shape of the file passed to `channel-send`. A field in `<...>` is filled in. Every other line is sent unchanged. `channel-send` rejects a body that drops a verbatim line or leaves a `<...>` field unfilled.

## Arrows

Above the diagram, three lines are enough:

```
# -> waits. One unremoved file from that sender, then the next send waits.
# -< does not wait. Files queue at the crow's foot, and that foot can be at either end.
# <-> is -> in both directions.
```

The background for those operators is [diagram-marks.md](diagram-marks.md).

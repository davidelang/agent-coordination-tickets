# Example

A planner and one coder. Each direction is one-at-a-time.

`comms.diag` is the picture and the key in one file. The three lines above the diagram are the arrow rules. The key is inside a `/* */` comment so blockdiag skips it. Every inbox path uses the dummy prefix `/channel`. Copy the file, replace that prefix, then `channel-setup -n` and `channel-setup` once per node.

```sh
channel-arm comms.diag coder
channel-arm comms.diag coder --whatineedtoknow
channel-send comms.diag planner coder IMPLEMENT body.md
rm /path/to/the/inbox/file
```

# Diagram marks

Background for the arrow rules in the [README](../README.md). The protocol uses three operators because they are easy to type. blockdiag can also set `hstyle` on an edge. That is harder to read in a diagram source, and this protocol does not.

The operators in blockdiag 3.0 are `->`, `<-`, `<->`, `--`, `-<`, `>-`, and `>-<`.

## What the three marks mean here

`->` and `-<` are the same inbox and the same rename. They differ when the sender already has a file there that the receiver has not removed.

`->` is one-at-a-time. The send script prints the pending filename and blocks until that file is removed, then sends. The sender sees why it is waiting. It does not poll.

`-<` is a queue. The script sends immediately. The crow's foot is on the destination, the end where the files pile up.

`<->` is the one-at-a-time rule in both directions.

## Standards

A flowchart arrow, ISO 5807, means the next step follows this one. That is `->`: one outstanding file, and the next send waits until that file is removed. A flowchart has no arrow for a pile of similar items.

Crow's-foot notation (James Martin) puts a single barb at an end for "one" and a foot for "many". `-<` is that foot at the destination: many messages of the same kind, waiting in one inbox. `>-<` would be a foot at both ends. The protocol does not use it.

A data-flow diagram (Yourdon, Gane-Sarson) would draw the pile as a store, a separate node between the two processes, with ordinary arrows in and out. The foot stands in for that store. The inbox directory is the store, so the diagram does not add a node for it.

A UML sequence diagram draws a filled arrowhead for a call that waits, and an open arrowhead for a send that does not. blockdiag's open triangle is `hstyle = generalization`. On a class diagram that triangle means inheritance. Using it for "async" would collide with that meaning, and it would not say "many". The foot already says many, and the send script's wait says one-at-a-time.

Composition (filled diamond) and aggregation (open diamond) are whole-part marks on a class diagram. They are not channels. `oneone` draws no heads. `<-` is `->` with the node names swapped. `--` is a line with no heads.

BPMN uses a filled arrow for the order of activities and a dashed open arrow for a message between pools. A dashed stroke in blockdiag changes the line only. The protocol does not treat it as a different channel.

# Diagram marks

Background for the arrow rules in the [README](../README.md). Two marks, typed as text, say everything a channel needs. blockdiag can also set `hstyle` on an edge. That is harder to read in a diagram source, and this protocol does not.

## What the marks mean here

`->` and `-<` are the same inbox and the same rename. They differ when the sender already has a file there that the receiver has not removed.

`->` is one in flight. The send script prints `PENDING` with the earlier filename and blocks until that file is removed, then sends. The sender sees why it is waiting. It does not poll.

`-<` is a queue: many messages may wait. The script sends immediately. The crow's foot is on the destination, the end where the files pile up.

The mark always sits at the receiving end. At the right end, `>` is an arrowhead (wait) and `<` is a crow's foot (queue). At the left end it is the mirror image: `<` waits and `>` queues. An end with no mark means nothing is sent toward it.

One line can carry both directions: `<->` waits both ways, `>-<` queues both ways, and `>->` waits left to right and queues right to left (`<-<` is the reverse). `A <- B` is `B -> A`, and `A >- B` is `B -< A`.

`planner >-> coder;` is the usual shape: the planner hands out one task at a time and waits for the coder to finish it, while the coder's reports queue at the planner and never block the coder.

`--` has no mark at either end, so it is not a channel and the tools reject it. Any other text made of `<`, `-`, and `>` is rejected too, and so are two lines that give the same direction different rules.

## What blockdiag draws

blockdiag 3.0 parses `->`, `<-`, `<->`, `--`, `-<`, `>-`, and `>-<`. It does not parse `>->` or `<-<`: both stop with `Got unexpected token`, and its current upstream source is the same. A plan that uses them works with these tools but does not render in blockdiag until blockdiag accepts them; write the two directions as two lines (`planner -> coder;` and `coder -< planner;`) when the drawing matters.

## Standards

A flowchart arrow, ISO 5807, means the next step follows this one. That is `->`: one outstanding file, and the next send waits until that file is removed. A flowchart has no arrow for a pile of similar items.

Crow's-foot notation (James Martin) puts a single barb at an end for "one" and a foot for "many". `-<` is that foot at the destination: many messages of the same kind, waiting in one inbox. `>-<` is a foot at both ends: a queue each way.

A data-flow diagram (Yourdon, Gane-Sarson) would draw the pile as a store, a separate node between the two processes, with ordinary arrows in and out. The foot stands in for that store. The inbox directory is the store, so the diagram does not add a node for it.

A UML sequence diagram draws a filled arrowhead for a call that waits, and an open arrowhead for a send that does not. blockdiag's open triangle is `hstyle = generalization`. On a class diagram that triangle means inheritance. Using it for "async" would collide with that meaning, and it would not say "many". The foot already says many, and the send script's wait says one-at-a-time.

Composition (filled diamond) and aggregation (open diamond) are whole-part marks on a class diagram. They are not channels. `oneone` draws no heads. `--` is a line with no heads, and here it is an error.

BPMN uses a filled arrow for the order of activities and a dashed open arrow for a message between pools. A dashed stroke in blockdiag changes the line only. The protocol does not treat it as a different channel.

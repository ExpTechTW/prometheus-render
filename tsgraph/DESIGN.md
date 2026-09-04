# Design notes

`tsgraph` draws in the style of RRDtool and MRTG. It does not contain their
code: the axis and scaling algorithms here are its own. The package was named
`rrdgraph` while it was still a port; it is not one any more, and the name went
with it.

## The vertical scale

Heckbert's nice-number method (Paul S. Heckbert, "Nice Numbers for Graph
Labels", *Graphics Gems*, 1990): round the range to 1, 2 or 5 times a power of
ten, choose a step the same way, snap the ends outwards to whole steps.

Two rules on top of it:

- a series that never goes negative is drawn against a zero baseline, because a
  traffic graph starting at 7.54 invites the reader to misjudge the ratio
- labels thin out to every second or fifth gridline once they would collide with
  each other, so the axis never prints text over itself

## The time axis

A ladder of intervals people read time in — 1/5/15/30 seconds, 1/5/15/30
minutes, 1/2/3/6/12 hours, 1/2 days, 1/2 weeks, 1/2/3/6 months, 1/2/5 years —
each with a grid subdivision and a label format. The first rung whose labels
have room for their own text is taken.

The room needed is per rung, not a single constant: `%H:%M` is five characters
and can sit closer than `%a %H:%M` at nine. Sizing them all to the widest format
skips a rung and halves the number of labels.

Calendar units are counted rather than converted to durations, so months keep
their own lengths and a daily tick stays on midnight across a clock change.

## Where this lands next to RRDtool

Given the same windows at 500px wide, the ladder picks the same interval as
RRDtool at all four of the timescales the gallery uses. The labels differ at
one: over 35 days this shows the date, where RRDtool shows an ISO week number.

## What is deliberately not implemented

`VDEF`, RPN and `CDEF` evaluation, reading RRD files, logarithmic axes, a
right-hand axis, and SVG/PDF/EPS output. PromQL covers the first three for this
repository's purposes.

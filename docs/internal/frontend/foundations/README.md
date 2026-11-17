# Foundations

The scales every component is built from. Each page opens on its reference tables, with what holds
each rule (see [`../standard.md`](../standard.md) for the tags), and keeps the reasoning that used
to be its `design-system.md` section underneath.

| Page | Holds | Was |
| --- | --- | --- |
| [`tokens.md`](tokens.md) | Every token family in `globals.css` and what it is for | new |
| [`colour.md`](colour.md) | One mode, the three inks, the brand and the signals, tints, chart hues, the control edge | §1, §3 |
| [`surfaces.md`](surfaces.md) | Grounds, borders, hairlines, elevation, radius, which blocks are framed | §2, §9 |
| [`states.md`](states.md) | Selected, hover and focus; the focus ring; row actions | §6 |
| [`type.md`](type.md) | Type roles, weights, inks, caps labels, figures | §8 |
| [`spacing.md`](spacing.md) | Spacing roles | new |
| [`motion.md`](motion.md) | The four meanings, durations, easing, reduced motion, the pause | §11 |
| [`words.md`](words.md) | One vocabulary per idea, no descriptions under titles, a verb is a word | §4, §5, §13 |
| [`layout-phone.md`](layout-phone.md) | Breakpoints, container queries, z layers, control sizes, touch, the phone | new |

These pages describe the scales as 0.9.0 accepts them (the UI-system plan of 15 Nov 2026, with the
operator's answers to T1, T2, T3 and T9), and `globals.css` holds them. [`tokens.md`](tokens.md) is
printed from the stylesheet and checked against it, so it is the reference for a value; where a
rule here and the stylesheet differ, the stylesheet is the bug.

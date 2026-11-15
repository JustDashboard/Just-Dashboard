# The identity line

A page opens on what it is about (D1).

WP-41 cuts this page down to the rule; until then it holds the text that was in `design-system.md`
§14, below. The dated stories it tells are also why the pages in
[`../decisions/`](../decisions/README.md) look as they do.

## 14. A header is its title

**A surface's header carries its name and its actions. It does not carry a picture of itself.**

**The outline a screen reader walks is part of the header.** Every page has exactly one `h1`: a
workbench that draws no header (Files, Terminal) says its name in an `sr-only` one. Headings never
skip a level, so a `FormSection`'s title is an `h2`, the level under the page's title; it was an `h3`
with nothing between. Nothing is drawn outside a landmark: the sidebar's wordmark, version and search
are the page's `banner`. A scrolling table with nothing in it to focus is itself a keyboard stop
(`Table` makes its container a named, focusable region only then, so a table of links keeps the tab
order it had). A mark that is a link says what it opens in words when its title is drawn rather than
written (a topology node's `name`), a status dot that carries a label is `role="img"`, and a
`Sparkline` without a label is hidden rather than an unnamed image. `design-system.spec.ts` holds the
first three and `network-topology-interfaces.spec.ts` the table and the node's name.

Every `PanelHeader`, `Modal`, `SidePanel` and `Section` used to open with a 28px brand-tinted plot
and a glyph inside it. On a page of six panels that is six brand marks down the left edge, each one
the same weight as the one control the reader is actually meant to press — and none of them said
anything the word beside them did not. `Servers` in front of "Filesystems", `Cpu` in front of
"Processor", `ShieldOff` in front of "Health": a glyph is a guess at a word the header has already
spelled out. Where the picture and the title disagreed, the title was the one that was right.

The prop is gone from all four components, so this one is enforced by the type checker rather than by
judgement: there is no `icon` to pass. `ChartPanel` forwarded one too, and does not any more. Nor does
`StatTile`: the 12px glyph it drew before its eyebrow was the same guess at a smaller size — `Cpu` in
front of "CPU" is the label twice — and twenty-eight tiles across the product carried one.

`Notice` kept the last one, and it is gone too. A notice's glyph is allowed — a severity is a thing a
shape can say, which is the exception below — but the 28px tinted square around it never was. On a
toned banner it was the fourth tinted object in a box that needed one: a wash, a rule, a plate, and
the glyph on the plate, all the same hue, to say that a certificate was fine. The glyph now stands on
the banner's own ground at the size of the title's line, and the banner is `rounded-lg` — §9's fence
step, beside `Group`, which is what it is — rather than the block step beside `Panel`, which it is
not. **And a `Notice` is for what the reader has to act on.** `/deploy/new`'s public address drew
three: one saying HTTPS was ready, which repeated the field's own hint word for word; one saying a
certificate would be issued, which nobody has to do anything about; and one saying automatic HTTPS
needed attention, which is the only one of the three that asks for a decision. The restart record on
`/dashboard/configuration` had four more — amber "do not reload" while it ran, green "the dashboard
has moved", amber "worth knowing", red "what went wrong" — and none asked for a decision either: the
state is now the mark beside the headline, the guidance one line of hint, and the error the line of
the transcript that failed, washed where it sits. A state is a `Status`
beside the thing it is a state of; what will happen by itself is a line of hint; a tinted box is for
the third case.

What the header has instead: the title at `text-title` (§8), the header's own ground and hairline
doing the separating, and — where there is something to say about state — a `Status` in the actions.
That was always the honest mark, because its colour is a reading rather than a decoration.

**Icons did not go away; decorative icons did.** A glyph stays wherever it *is* the message:

- a `Status` verdict, a `Notice` severity, an `EmptyState`'s mark — the shape carries a tone the words
  would need a sentence for;
- a verb on a button or in a row's actions, under the rules in §13;
- **wayfinding** — the sidebar entry, the overview's module tiles, the security page's area rows.
  These are the same glyph the reader is about to click through to, held steady across the product so
  the eye can find "Docker" without reading. A header is not wayfinding: you are already there.

**A wayfinding mark may carry its own colour, and only a wayfinding mark.** A repository list is
scanned rather than read, and after the name the language is what decides which of forty rows is the
one — so `LanguageMark` draws the language's logo in front of its word. In `text-muted-foreground`
that glyph was the same grey as the forty words around it: a mark doing the half of its job that
costs pixels and none of the half that saves a read, because a column of twenty identical grey
glyphs is a texture and the reader goes back to reading the words. The hue is not this product's to
choose — it is GitHub Linguist's, the colour the same repository carries on the site the list was
fetched from, which is the argument §3 makes for `--brand` applied to somebody else's mark. The
lightness *is* ours, and it is one rule rather than twenty judgements: every `--language-*` in
`globals.css` is the logo's hue and chroma at L 0.72, the rung `--tag-*` already sits on, because
Linguist's values were picked for a white page and four of them (Lua's navy, Ruby's oxblood,
Markdown's ink, C's grey) are invisible on a 0.16 ground. The rule is written out as thirty literal
`oklch()` values carrying their source hex in a comment rather than stated once as
`oklch(from <hex> …)`: relative colour syntax is the one modern colour function Lightning CSS cannot
downlevel, so those thirty would have been the only tokens in the file shipping without a fallback,
below the floor Next's default browserslist target declares. Deleting the lightness dimension has a
price, and it is paid by the pairs Linguist separated by lightness alone — Lua and Markdown come out
as the same blue. The glyph shapes and the word beside them still tell those two rows apart, and a
floor high enough to be legible on this ground collapses them either way, so the rule stands as
written. This
does not extend to the marks a reader is choosing *between*: the four source kinds on `/deploy/new`
stay muted with the current one in `--brand`, because there the colour is saying which one you are
on (§3), and twenty hues in a row of four would be saying nothing.

### Any page can open as a project does

**Any page can open as a project does.** The project's header was markup in `ProjectShell` and the
other pages' line was `HostIdentity`; since 0.8.7 the header is `HostIdentity` too. Given a `state`, a
`command` or a `menu` it takes the project's layout: a 40px tile beside two rows, the title with its
state after it, the facts under them, the command and its `⋯` at the far end across both, 16px above the
hairline; on a phone the tile, the title and the command keep the first row, the state takes the next
and the facts run the full width. `label` makes the line a landmark ("About api"). Without those props
the line is drawn as it always was — a 48px tile, the facts under the title, the `aside` at the far
end — so a page opts in by passing them.

### A pane's strip keeps a small glyph

A `Pane`'s chrome strip is the one place a small inline glyph still sits beside a name (the git tools
column, the session rail). A pane is a region of a workspace rather than a block of content, its strip
is deliberately tighter than a panel's, and the mark there is a bare 14px outline rather than a tinted
plate — it reads as part of the chrome, which is what it is.

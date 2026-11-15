# Overlays and confirmations

Dialogs, sheets, popovers and the confirmation that names its subject.

**Members:** `Modal`, `PaletteModal`, `SidePanel`, `ConfirmDialog`, `ui/popover`, `ui/hover-card`,
`ui/tooltip`, and `Explain` (coming in WP-03).

**The rule (D9).** An item's detail is a page, not a sheet: a route of its own, its views named in
`?tab=`, and from `xl` a rail of its siblings
([`../recipes/detail-with-rail.md`](../recipes/detail-with-rail.md)). The address that opened a
sheet redirects to the page. A `SidePanel` is for a small add or edit form, and its anatomy is in
`SidePanel`'s doc comment. `Modal`, `PaletteModal` and `SidePanel` are the only assemblers of the
raw `Dialog` and `Sheet`, and `eslint.config.mjs` refuses the import anywhere else.

**Status: stub.** WP-15 writes this page in wave 1: purpose, anatomy, use when and when not (and
what instead), props, states, the phone, accessibility, an example call site, what enforces it, and
related families. Until then the rules are in [`forms.md`](forms.md) (*A sheet is the same anatomy
wherever it opens*, *A confirmation names its subject*) and [`README.md`](README.md) (the `Modal`
and `SidePanel` entries).

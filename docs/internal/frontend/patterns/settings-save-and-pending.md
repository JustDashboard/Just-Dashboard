# Settings: save and pending

Settings are one centred column under one floating save bar, with a strip for what is stored and
not in force yet (D8). There is no autosave (T8 was not adopted).

A reader changes three things two screens apart and then looks for Save. One bar, in one place,
says how many edits the page holds, where they are and when they take effect.

## The three states of a setting

| State | Where it is said | With |
| --- | --- | --- |
| **Typed, not saved** | the floating bar at the foot (the count, the parts the edits are in, when they apply, Discard and Save), and *Unsaved changes* at the far end of each section's head | `SaveBarProvider`, `SettingForm`, `settingStatus`, `EditedTag` |
| **Saved, not in force** | a strip at the head of the column, with each pending thing and where it is changed | `PendingStrip` |
| **Live** | nothing. A "Live" on every head would be noise | |

## The parts

- **`SettingsColumn`** (`components/settings/settings-column.tsx`): the one centred column at the
  fields' width, the strip at its head and room at its foot for the bar. Every settings page is
  drawn in it, so their widths cannot drift.
- **`SettingForm`** (`components/settings/setting-card.tsx`): one `<form>` and one write. It draws
  no Save. It puts itself on the page's bar with whether it is dirty, how many edits it holds and
  its `onSave`, which resolves true when the write went through. It may span several sections.
- **`SettingSection`**: a section's head (title, what the section is now as data, one status at
  the far end, small outline actions) and its fields ([`sections.md`](sections.md)).
- **The bar** (`components/settings/save-bar.tsx`, drawn with `FloatingBar`): the one thing on a
  settings page that floats, so it takes a popover's surface and shadow. Save writes each dirty
  form in page order. Ctrl+S or ⌘S (`SaveKey`) and Enter in a field are the same Save.
- **`PendingStrip`** (`components/settings/pending-strip.tsx`): a headline, a line of detail and
  the pending items, each a link to where it is changed. It has no button of its own: the page's
  command takes the items live.
- **`DangerRows`** and **`DangerRow`**: the page's only framed block, one edge around every
  destructive act, each with its sentence and its one button.

## The rules

1. **One Save on a page.** A form never ends on its own button.
2. **The bar says when it applies.** `applies` on the form is `immediately`, `next-reload`,
   `restart` or `next-deployment`, and the bar's line says it in words (`appliesLine`).
3. **A save that needs an answer waits for it.** Where saving replaces a container or turns
   something off, the bar asks first; Save waits on the confirmation, and a cancelled or refused
   change leaves the draft as it was chosen ([`confirmations.md`](confirmations.md)).
4. **A refusal stays with the form.** The server's words stand over the form that was refused, the
   field it names is marked, and the draft is kept.
5. **The strip arrives for what is stored, never for what is typed.** A strip that came with the
   first keystroke would push the field being typed in down a line. Unsaved edits are the bar's and
   the heads'.
6. **A switch is one line.** `SwitchRow`: what it switches at the left, the switch at the far end,
   the reasoning behind the ⓘ beside its name. A small closed choice is `Segments`. A setting with
   a shape gets a `SettingPicture`.
7. **A draft outlives a visit to another page.** A reader who comes back sees which heads still
   hold unsaved edits.
8. **A setting on a reading page takes the same bar.** The page mounts a `SaveBarProvider` around
   one `SettingSection`. A column beside a rail passes `inline`, so the bar holds to the foot of
   the column it saves instead of floating over the area.
9. **An account that cannot change a setting reads its value** in the control's place, and the bar
   never appears.

## The phone

The column is the screen's width. The bar stands above a pane bar when the page has one (the bottom
stack, [`../components/overlays-confirm.md`](../components/overlays-confirm.md)), and the strip's
items fold under its headline so the first field stays on the first screen.

## Accessibility

Each form is named (`name`), which is how a screen reader, a spec and the bar's line find it. The
bar is a named region whose count is announced politely. A section's head is an `h2`, and a danger
row's title an `h3`.

## Held by

`deploy-settings-a.spec.ts` and `deploy-settings-b.spec.ts` (the column, the bar, the strip),
`design-system.spec.ts` (*a settings page's sections are the level under its title*). The rest is
review.

## Related

[`../recipes/settings-page.md`](../recipes/settings-page.md),
[`../components/forms.md`](../components/forms.md), [`sections.md`](sections.md),
[`confirmations.md`](confirmations.md).

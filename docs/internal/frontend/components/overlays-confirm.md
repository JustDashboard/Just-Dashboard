# Overlays and confirmations

Dialogs, sheets, the viewer, the confirmation and the one-name prompt; tooltips and explanations;
and what floats at the foot of the window: the save bar and the docks.

**Members:** `Modal`, `ModalDescription`, `DialogSubject`, `StageModal`, `PaletteModal`
(`components/modal.tsx`); `SidePanel`, `SidePanelFooter`, `useInSidePanel`
(`components/side-panel.tsx`); `ConfirmDialog`, `useConfirm` (`components/confirm-dialog.tsx`);
`PromptDialog`, `usePrompt` (`components/prompt-dialog.tsx`); `ImageLightbox`
(`components/image-lightbox.tsx`); `FloatingBar`, `SaveKey`, `useFloatingBarHost`
(`components/floating-bar.tsx`); `Dock`, `useStackOccupant`, `useBottomStack`, `stackOffsets`
(`components/dock.tsx`); `useFullscreen` (`hooks/use-fullscreen.ts`); `ui/popover`,
`ui/hover-card`, `ui/tooltip`, and `Explain` (`components/explain.tsx`).

## The rule (D9)

An item's detail is a page, not a sheet: a route of its own, its views named in `?tab=`, and from
`xl` a rail of its siblings ([`../recipes/detail-with-rail.md`](../recipes/detail-with-rail.md)).
The address that opened a sheet redirects to the page. An overlay is for a short task that ends:
a small add or edit form, a question, a name, a picture.

| The task | Use | Not |
| --- | --- | --- |
| create, rename, import, edit one row | `Modal` | a page of its own for one field |
| a small add or edit form beside the page it belongs to | `SidePanel` | a sheet for an item's detail (a page) |
| a destructive or hard-to-undo act | `ConfirmDialog` through `useConfirm` | a `Modal` with a red button |
| one name and one verb | `PromptDialog` or `usePrompt` | a `Modal` with an `Input` |
| a picture as large as the window | `ImageLightbox` (on `StageModal`) | `Modal size="full"` |
| search that is its own header | `PaletteModal` | a `Modal` with an input in the body |
| the name of an icon control | `Tooltip` (through `IconAction`) | a `title` attribute |
| a reason a reader needs explained | `Explain` ([`forms.md`](forms.md)) | a tooltip or a hover card |

`Modal`, `PaletteModal`, `StageModal` and `SidePanel` are the only assemblers of the raw `Dialog`
and `Sheet`, and `eslint.config.mjs` refuses `@/components/ui/dialog` and `@/components/ui/sheet`
anywhere else. `ui/sheet.tsx` reads two class strings from `ui/dialog.tsx` (`OVERLAY_CLOSE`,
`OVERLAY_GROUND`) and nothing else, so the two strips put their cross in one place and the two
grounds fade alike.

## Size: one vocabulary

`Modal` and `SidePanel` both take `size`, with the steps `sm`, `md`, `lg` and `xl` (`OverlaySize`).
The name says what the overlay holds, and each surface turns it into its own width:

| `size` | Holds | `Modal` | `SidePanel` |
| --- | --- | --- | --- |
| `sm` | a name, a sentence and a button: a rename, a confirmation | 28rem | 32rem |
| `md` | a form of a few fields | 32rem (default) | 42rem |
| `lg` | a form with a list, a table or a block of code | 42rem | 48rem (default) |
| `xl` | an editor or a table that wants the width | 56rem | 64rem |
| `full` | the whole window: the file viewer | `Modal` only | — |

A sheet is a column the height of the window, so each of its steps is the wider one. `SidePanel`'s
`width` is the old name for `size` and is deprecated.

## Modal

- **Anatomy.** A title on a tinted strip with a hairline under it (`actions` beside the close
  button), a body that is the only part that scrolls, and a footer strip with the buttons, Cancel
  (outline or ghost) and then the one command on the right. `progress` draws the action in flight
  in the footer: its words beside the buttons and its track along the footer's top edge.
- **Body, in order.** The subject first (`DialogSubject`), kinds as `ChoiceCard`s, groups as
  `FormSection`s under hairlines, options as an `OptionList`. The order is written once, at
  `SidePanel`'s doc comment, and holds for both.
- **Props.** `open`, `onOpenChange`, `title`, `description` (read to a screen reader, never
  drawn), `size`, `variant` (`form` or `alert`), `actions`, `footer`, `progress`, `initialFocus`
  (`first`, `body`, or a ref to the control the keyboard lands on), `onCloseAutoFocus`,
  `bodyClassName`.
- **`variant="alert"`** is for a question that interrupts. It is announced as an alert dialog, a
  press outside it is not an answer (Cancel, the cross and Escape are), and its sentence is drawn in
  the body through `ModalDescription`, which is the dialog's accessible description.
- **Focus.** It opens on its first field, and on close the keyboard goes back to what opened it
  (`useFocusReturn`), trigger or not. A page that removes the opener with the dialog's own act (a
  key's Delete) also runs `useFocusTrail`, which acts only when nothing holds the keyboard after
  the dialog leaves.
- **The phone.** The box is the window less a 1rem gutter, capped at the window's height, and the
  body scrolls inside the frame, so the footer's buttons are always reachable.

```tsx
<Modal open={open} onOpenChange={setOpen} title="Rename volume" size="sm" footer={footer}>
  <DialogSubject mark={<ProductLogo id="docker" size="sm" />} name={volume.name} mono />
  <Field label="New name">…</Field>
</Modal>
```

## DialogSubject

The thing a dialog acts on, drawn as itself at the head of the body: its mark (a `ProductLogo`,
`ProjectMark` or `EngineMark` at `size="sm"`), its name (`mono` for an identifier) and the facts
that tell it from its neighbours as `FormFact`s. A wrong subject is caught when it is recognised,
before it is read (§14). `ConfirmDialog` draws its `subject` with it; a dialog that edits one thing
opens on it the same way rather than composing its own.

## ConfirmDialog and useConfirm

The confirmation for a destructive or hard-to-undo act, in two forms: a plain question, and the same
question with a phrase to type (`phrase`) for the kinds the server checks a phrase for (permanent
project deletion, a whole database, a Docker stack, a Git checkout, a home with no archive). The
server re-decides either way; this is a guard against a slip, not the enforcement point.

- **Request.** `title` (the verb and the kind: "Remove volume"), `description` (what will happen,
  in plain words), `subject` (`DialogSubject`'s props), `phrase`, `confirmLabel`, `workingLabel`,
  `action(confirm, report)`, `onDone`.
- **States.** At rest; typing the phrase (a tick at the field's end when it matches, the button
  live); running (`progress` in the footer, the dialog held open); failed (a toast with the reason,
  the dialog still open). An action that reports its own outcome resolves to `"reported"`.
- **Accessibility.** An alert `Modal`: no dismissal by a press outside, the sentence as its
  description, and the keyboard on Cancel — the answer that loses nothing — unless there is a phrase
  to type. The button is always destructive.
- **Role.** It is announced with `role="dialog"` for now: some three hundred browser spec lines
  find a confirmation as `getByRole("dialog")`, and to Playwright an `alertdialog` is not one. The
  role flips with one line in `confirm-dialog.tsx`, together with those specs.

```tsx
const { confirm, dialog } = useConfirm()
confirm({
  title: "Remove volume",
  description: <p>The volume and everything in it are deleted. Containers using it stop.</p>,
  subject: { mark: <ProductLogo id="docker" size="sm" />, name: volume.name },
  action: () => del(`/docker/volumes/${volume.name}`),
})
```

## PromptDialog and usePrompt

One name, sometimes a message, and one verb: Files' rename, new folder, new file; Git's branch from a
commit, tag, rename, stash label. `PromptDialog` is driven by props (`open`, `onOpenChange` and the
options); `usePrompt()` returns `{ prompt, dialog }` for a caller that asks imperatively.

- **Options.** `title`, `label`, `hint` (a line, or a function of what is typed, such as the path a
  new folder lands at), `placeholder`, `initial`, `verb` (required, one word), `facts` (what the
  name is given to), `message` (a second field), `validate` (a refusal said under the field),
  `onSubmit(value, message)`, `refusal` (the title of a server's refusal), `selectBasename`,
  `mono`.
- **States.** A refusal from `validate` is the field's error, and the verb is disabled; a throw from
  `onSubmit` keeps the dialog open with the reason in a `Notice` above the field, so the name can be
  changed and sent again. Enter submits; the verb is the form's submit button.
- **Retired.** `files/prompt-dialog.tsx` (`usePrompt` with `submit` and `confirmLabel`) and
  `git/name-dialog.tsx` (`NameDialog` with `confirmLabel`) are thin wrappers over this one, marked
  deprecated, until their pages move.

## SidePanel

The sheet: a small add or edit form that opens over the page it belongs to — a credential, a
channel, a webhook, a mount. Not where an item's detail goes (D9). The same anatomy as `Modal`:
a title strip, a body that scrolls, a footer strip; a body that brings its own command puts it in
the footer with `SidePanelFooter`. Its full anatomy is its doc comment. Props as `Modal`'s, with
`size` (default `lg`) and without `progress`.

## StageModal and ImageLightbox

`StageModal` is a viewer that takes the whole window on a dark ground: a strip of white controls
(`title`, `actions`, the cross) and the stage under it. It is still a dialog — the keyboard held
inside, Escape and the cross close it, the title is its accessible name, the keyboard goes back to
what opened it. `ImageLightbox` is the one viewer on it: arrows, the keys or a swipe walk the
pictures, a click on a picture larger than the window shows it pixel for pixel, and a press on the
ground closes it.

## PaletteModal

A search overlay whose input is its title: the command palette. No title strip and no close button;
Escape and a press outside dismiss it. It hangs from a fixed line near the top so it does not jump
as its results grow and shrink.

## Tooltips, hover cards and explanations

A `Tooltip` is the name of an icon control, and it is hoverable (WCAG 1.4.13): the pointer can move
onto the bubble. A strip the pointer scrubs, such as the deploy insights day strip, passes
`disableHoverableContent`, or the last bar's bubble holds while the pointer crosses to the next. A
`HoverCard` is a decorative preview only. Neither opens for a finger, so anything a reader needs
explained is an `Explain` ([`forms.md`](forms.md)): opened by hover or focus it is a `tooltip`
describing its mark; pinned by a press or a tap it is a small `dialog` named by its label.

## The bottom stack: save bar and docks

What floats at the foot of the window is one stack, in the float layer (`z-float`), and nothing in
it covers anything else.

- **Bars** span the foot: `FloatingBar` (the settings save bar, a proxy site's two-save bar) and a
  pane page's bottom bar on a phone (`PaneBar`, 56px and the home indicator). A bar reports how far
  up from the window's edge it reaches (`useStackOccupant(element, "bar")`). A bar of a higher
  `order` stands above the ones under it, lifted by how far they reach: the save bar is order 1 and
  a `PaneBar` order 0, so the two on one phone page stand one above the other rather than one on
  the other's stops. The stack takes the tallest reach, which is the upper bar's: a bar is measured
  again when its size changes and when its `style` does, since a lift moves its reach without
  changing its size (`watchOccupant`, `lib/stack-occupant.ts`).
- **Docks** sit in the bottom-right corner above every bar, and on each other in `order`, each
  lifted by the measured height of the ones below: Remote Desktop's held sessions (`order` 0, the
  corner first), then the assistant's conversations (`order` 1). `Dock` is the frame: a popover's
  surface, 22rem wide and the window less its gutters on a phone, rising in and sinking away.
- **What the page gets.** The stack publishes `--bottom-occupied` (what the bars cover) and
  `--bottom-stack` (the top of everything, docks included) on the root. The shell's scroller takes
  a `scroll-padding-bottom` of the first in `globals.css`, so a focused field near the foot of a
  long form is scrolled clear of the save bar instead of sitting under it.
- **`stackOffsets`** is the arithmetic and `watchOccupant` the measuring, both unit-tested in
  `components/dock.test.js`.

`FloatingBar({ headline, note, state, actions, width, position, label })`: `headline` is what there
is to save and shimmers while saving; `note` is what saving does, one line (two on a phone);
`state` is `dirty`, `saving` or `saved`; `width` is `md`, or `lg` for a third command; `position` is
`viewport` (over the content area, centred on it) or `sticky` (held to the foot of the column it is
in, beside a rail). `SaveKey` is the primary command's keycap. The logic of what to save stays with
each bar: `SaveBarProvider` (many forms, one Save; [`forms.md`](forms.md)) and `SiteSaveBar`.

## Full screen

`useFullscreen(root, { escape })` returns `{ full, fullscreen, immersive, toggle, leave }`. It asks
for the browser's full screen and, where that is refused, sets `immersive`, which the caller draws
as `fixed inset-0 z-immersive`. In the immersive overlay everything outside `root` is inert, Escape
leaves (unless the press was already an answer to a menu, a dialog, a field, or a handler that
called `preventDefault`), and the keyboard goes back to the toggle. `escape: false` is for a surface
whose Escape is its own key: the terminal and a remote desktop. The server map and a database's
diagram use it.

## What enforces it

- `eslint.config.mjs`: `no-restricted-imports` on `@/components/ui/dialog` and
  `@/components/ui/sheet` outside their assemblers.
- `components/verbs.test.js`, `components/dock.test.js`: the danger ink and the stack's arithmetic.
- Browser specs: `shared-dropdowns`, `files-ui`, `git-ui`, `assistant`, `deploy-settings-a`,
  `proxy-site-builder`, `server-map`, `database-schema`.

**Related:** [`menus.md`](menus.md) (the verbs a confirmation follows), [`forms.md`](forms.md)
(fields, `SaveBarProvider`, the settings column), [`../foundations/layout-phone.md`](../foundations/layout-phone.md)
(the layers).

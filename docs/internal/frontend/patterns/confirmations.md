# Confirmations

A confirmation names its subject, says what will happen in one sentence, and its button repeats the
verb.

A confirmation is the last place a wrong press is caught. "Are you sure?" over an OK button catches
nothing. The thing drawn as itself, its name, and "Remove credential" on the button do.

## What draws it

| The question | Use |
| --- | --- |
| "Do this to that?" | `useConfirm()`, which opens the one `ConfirmDialog` from anywhere in a page |
| A name or one value before the act | `usePrompt()` and `PromptDialog` |
| An act with a form of its own (a restore's options) | a `Modal variant="alert"` built from the form kit, its subject in a `DialogSubject` |

A page never assembles a raw dialog. The anatomy is in
[`../components/overlays-confirm.md`](../components/overlays-confirm.md).

## What the request says

`ConfirmRequest` (`components/confirm-dialog.tsx`):

- **`title`**: the act, as its verb and its kind: "Remove container".
- **`subject`**: the thing it acts on, drawn as itself above the sentence: its `mark`, its `name`
  and the `facts` that tell it from its neighbours. A wrong one is recognised before it is read.
- **`description`**: what will happen, in plain words, and what it removes or stops. For a
  destructive act it says what is kept as well as what goes.
- **`confirmLabel`**: the verb again ("Remove", "Stop"), never "OK" or "Yes". `workingLabel` is
  what the button says while the act runs.
- **`phrase`**: a phrase to type. Only the few acts that cannot be undone and take much with them
  use it: deleting a project for good, deleting a whole database, removing a Docker stack, deleting
  a Git checkout from the server, deleting an account's home with no archive. The typed phrase is
  a `Field` like any other, and the button is live once it matches.
- **`action`**: runs it. It reports its progress, and the dialog draws that in its footer.

## The rules

1. **Ask only where a slip costs something.** Start, restart, save and copy do not ask. Stopping
   what serves traffic, removing, deleting, dropping and revoking do.
2. **The server decides.** A confirmation guards against a slip. It is never the enforcement
   point: the server checks the capability, and the phrase where there is one, again.
3. **The destructive verb is not the brand's face.** It is behind the `⋯` on the identity line, in
   the destructive ink, and the confirmation's button is the destructive variant
   ([`identity-line.md`](identity-line.md)).
4. **It interrupts, and says so.** It is an alert dialog: a press outside is not an answer. Cancel,
   the cross and Escape are. It opens on Cancel (or on the phrase's field), so Enter never
   confirms by accident. Its sentence is the dialog's accessible description.
5. **While it works, it stays.** The footer shows the act's name and elapsed time, and a measured
   count where the server reports one (`OperationProgress`). It never invents a percentage.
6. **A failure leaves the question open.** The working state clears, the server's words are shown,
   and the reader may try again on purpose.
7. **The keyboard goes back where it was.** On close, focus returns to what opened the dialog, or
   to the menu's button when the opener was a menu row (`useFocusReturn`). A page whose act removes
   the opener also runs `useFocusTrail`.
8. **After it is done, say so once.** A toast away from where the reader is looking
   (`notify.success`), or the row leaving the list. Not both, and not a second dialog.
9. **A save that needs an answer asks from the save bar**, which waits on it
   ([`settings-save-and-pending.md`](settings-save-and-pending.md)).

## Held by

Lint keeps raw `Dialog` and `Sheet` inside `Modal`, `PaletteModal` and `SidePanel`. The page specs
press each destructive verb and read the dialog by its title and its subject. Whether an act
should ask is review.

## Related

[`../components/overlays-confirm.md`](../components/overlays-confirm.md),
[`../components/forms.md`](../components/forms.md) (*A confirmation names its subject*),
[`../components/feedback.md`](../components/feedback.md), [`data-flow.md`](data-flow.md).

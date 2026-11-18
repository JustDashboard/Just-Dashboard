# Forms and settings

Fields, options, settings sections, the save bar, the pending strip and the danger zone (D8).

**Members:** `Field`, `FieldRow`, `FieldCheck`, `FieldChecks`, `useFieldControl`, `useField`,
`FormSection`, `FormSections`, `OptionList`, `OptionRow`, `FormFacts`, `FormNote`, `Statement`,
`Disclosure` (`components/form.tsx`); `SecretField` (`components/secret-field.tsx`); `ListField`
(`components/list-field.tsx`); `OneTimeCodeInput`, `RecoveryCodes`
(`components/account/one-time-code.tsx`); `SettingsColumn`, `SwitchRows`, `SwitchRow`, `EditedTag`,
`NoticeLines`, `NoticeLine` (`components/settings/settings-column.tsx`); `SettingsPage`
(`components/settings/settings-page.tsx`); `SettingForm`, `SettingSection`, `settingStatus`
(`components/settings/setting-card.tsx`); `DangerRows`, `DangerRow`
(`components/settings/danger-row.tsx`); `SaveBarProvider`, `useSaveBarEntry`
(`components/settings/save-bar.tsx`); `PendingStrip` (`components/settings/pending-strip.tsx`);
`generatePassword`, `passwordChecks` (`lib/password.ts`); `Segments`; `ui/input-group`.

**Status: current** for the parts below. The rules and their history follow from §7 on.

## Purpose

A form is a label, a control and one line under it, as many times as the task needs; a page that
*is* a form is one centred column of sections under one floating Save. These parts are the only way
the product draws either, so a field reads the same in a dialog, a sheet, a flow and a settings page,
and a screen reader hears its hint and its error wherever it is.

## Anatomy

- **`Field`**: the label (`text-body`, medium), an optional ⓘ (`info`) beside it, an optional
  `trailing` control at the label row's far end, the control, then one line: the hint, or the error
  (`role="alert"`, a warning glyph) while there is one. The field makes the ids and hands them to its
  control through a context (`useFieldControl`); `Input`, `Textarea`, `SelectTrigger`,
  `InputGroupInput`, `Switch` and `Checkbox` read it, and `ToggleGroup` takes the label's id as its
  name (`useField()?.labelId`), since a group is not a control a `for` can point at.
- **`SettingsColumn`**: `mx-auto max-w-3xl`, the pending strip first, then the sections in a
  `FormSections` (a hairline between each), and 80px at the foot for the save bar.
- **`SettingSection`**: a `FormSection aside`: an h2, under it what the section currently is as data
  (`state`), at its far end one `settingStatus` and the section's small outline buttons.
- **`DangerRows` and `DangerRow`**: the danger zone's one frame in `border-rule-danger`, a hairline
  between acts; each act is an optional mark (what it is done to, drawn as itself), an h3 title, one
  sentence, its facts, and its one button beside them.

## Use when, and when not

| You need | Use | Not |
| --- | --- | --- |
| A labelled control | `Field` around the control | `Label` + `Input` in a `space-y-1.5` div |
| Two or three short fields on one line | `FieldRow` | a hand grid |
| A rule the value must meet, lit as it is met | `FieldChecks` of `FieldCheck`s, inside the `Field` | a hand `aria-live` paragraph |
| A password the dashboard makes or the reader types | `SecretField` (show, generate, copy) | an `InputGroup` built again |
| A password made in code | `generatePassword()` from `lib/password.ts` | a second generator |
| Values added one at a time | `ListField`, with `parse` for a value that can be refused | a field and a tag list built again |
| An authenticator's six digits | `OneTimeCodeInput`, `size="flow"` on a sign-in step, `"reading"` elsewhere | an `Input` with its own `inputMode` and tracking |
| Recovery codes shown once | `RecoveryCodes` (`framed` in a settings column) | a `Well` and two buttons |
| A page that is a form | `SettingsColumn` (a deployment's: `SettingsPage`) | the column's classes typed again |
| A switch in a settings column | `SwitchRow` in `SwitchRows` | `OptionRow` |
| A switch on a task surface (dialog, sheet, flow) | `OptionRow` in an `OptionList` | `SwitchRow` |
| An act that cannot be undone | `DangerRow` in `DangerRows`, under a `SettingSection tone="danger"` | a red card per act |
| A closed set of two to five words | `Segments` | a `Select` |

**`SwitchRow` or `OptionRow`.** Both are a switch with its name, and they are kept apart on purpose.
A settings column is read top to bottom on every visit, so a switch there is one line: a short
label, the reason behind the ⓘ (`info`), nothing under it, and a row with a reason is as tall as one
without. That is `SwitchRow`. A task surface — a dialog, a sheet, a step of a flow — is read once,
while deciding, so its switch carries its sentence under the title. That is `OptionRow`. One rule
for every settings page: a short label, the explanation behind the ⓘ, and a visible line under a
field only where a mistake is costly or the value has a unit or a format. An explanation never goes
in the label after a dash ("Backup before deploy — the release waits for this job") — the dash
clause is the ⓘ's.

## Props

- `SecretField({ id, label, hint?, value, onChange, error?, shown? = true, generate? = true,
  copy? = true, length? })`. `shown` is how the value starts: a made password is shown, because the
  reader has to read or copy it; a typed one passes `shown={false}`. `length` is how long Generate
  makes one (`generatePassword`'s 30 otherwise).
- `generatePassword(length = 30)`: letters and digits from `PASSWORD_ALPHABET`, which every engine,
  shell and `htpasswd` line takes unquoted and which leaves out I, l, 1, O and 0.
- `ListField({ id, label, placeholder, values, onChange, hint?, info?, error?, disabled?, parse?,
  mono? = true })`. `parse(typed, values)` returns `{ entry }`, `{ error }` (shown as the field's
  error until the reader types again) or null (nothing added); without it an entry is trimmed and
  one already listed is skipped. `disabled` draws the values without their controls.
- `OneTimeCodeInput({ id?, value, onChange, size? = "reading", autoFocus?, required? })`: always
  `inputMode="numeric"`, `autoComplete="one-time-code"`, `maxLength={6}`; anything but a digit is
  dropped, so "123 456" pasted from a message arrives as the code.
- `RecoveryCodes({ codes, onDone, framed?, busy? })`.
- `SettingsColumn({ strip?, sectioned? = true, className?, children })`. `sectioned={false}` when
  the children already hold the strip and the sections, because one wrapper has to hold both (a
  database's `SettingsPending`, which the pages report to).
- `SettingsPage({ state, pageKinds?, children })`: a deployment's configuration read, then
  `SettingsColumn` with the pending strip and the last failure's remedy as its `strip`.
- `DangerRow({ mark?, title, sentence?, facts?, action?, children? })`. Without `action` the title
  goes quiet and the sentence says why the act cannot be taken, or what comes first. `children`
  outlives the row's own block, so the confirmation an act opens goes there.

## States

- **A field:** empty, typed, invalid (the error replaces the hint, the edge turns red through
  `aria-invalid`), disabled. Checks lit one by one while typing.
- **A settings column:** loading (the page's skeleton, final shape), clean (no bar), dirty (the bar
  rises with the count and what applies when), saving, refused (the form keeps its edits and says
  why under the field), saved.
- **A danger act:** open (its button), held (no button, quiet title, the sentence says why),
  not drawn (the role cannot take it). Stop turning into Start, or a held act getting its button,
  arrives rather than being repainted: the row's block is keyed on its title and state.

## The phone

Fields are 44px under a finger and 16px text, so iOS does not zoom (`Input`'s guard). `FieldRow`
stacks below `sm`. `SecretField`'s Generate and Copy keep their glyphs and lose their words. A
danger act's button goes full width under its sentence. The pending strip folds its items under its
headline. The code field keeps its height on a touch screen (`pointer-coarse:`).

## Accessibility

Every control in a `Field` is named by its label and described by its hint, its error and its
checks; the error is an alert once and the field stays described by it. `Checkbox` never takes the
field's id, because boxes come in runs each named by its own words; a `Switch` with its own
`aria-label` keeps the hint and error but not the id. `FieldChecks` is a polite live region, never
an alert. A danger act's title is an h3 under the zone's h2. The sign-in page's fields are all
`Field`s, so its "These do not match yet." is announced.

## Example

```tsx
<SettingSection id="danger" tone="danger" title="Danger zone">
  <DangerRows>
    <DangerRow
      mark={<ProductLogo id="docker" size="sm" />}
      title="Remove its container"
      sentence="Removes the container that runs this server, with the volumes it wrote to."
      facts={<>Container <span className="font-mono text-foreground">{name}</span></>}
      action={<Button variant="destructive" size="sm" onClick={remove}><WarningFill />Remove…</Button>}
    />
  </DangerRows>
</SettingSection>
```

## What enforces it

`components/form.test.js` (a field's wiring), `components/ui/field-controls.test.js` (Switch,
Checkbox and ToggleGroup in a field), `components/account/one-time-code.test.js`,
`components/settings/danger-row.test.js` and `lib/password.test.js`. The browser specs `sign-in`,
`auth-recovery`, `account-passkeys`, `database-operate` and `deploy-settings-b` fill these fields by
their labels. WP-39 adds lint rules against the retired names.

**Retired names, still exported until WP-39:** `SecretField` from `database/ops/access-parts`,
`generatePassword` from `database/connect/rules`, `SettingsPage` from `settings/setting-card`. The
proxy's `ListField` is a deprecated wrapper over the shared one, and the code fields on Passkeys
and the hub sign-in are `OneTimeCodeInput` now. Two local `SecretField`s remain, on the credentials
page and a webhook's sheet.

## Related

[`overlays-confirm.md`](overlays-confirm.md) (a sheet's anatomy, confirmations),
[`tabs-chips-segments.md`](tabs-chips-segments.md) (`Segments`, chips),
[`../patterns/settings-save-and-pending.md`](../patterns/settings-save-and-pending.md),
[`../recipes/settings-page.md`](../recipes/settings-page.md).

## 7. Which surface: what goes inside a task surface

**What goes inside a task surface is `components/form.tsx`.** A `Field` is a label at `text-body`,
a control, and one line under it — a hint, or the error while there is one; `FieldRow` puts two or
three side by side; `FormSection` opens a part of a longer form with a title and a hairline;
`OptionRow` is a switch with its sentence, because "Stop on error" as a checkbox label asks the reader
to guess what an error stops; `FormFacts` states what the form operates on as data under the title;
`Disclosure` is the part of a form that is folded away; and `Statement` shows the SQL a
schema-editing form is about to run, nearest the button that runs it.
A `Field` wires itself to its control: it makes the ids of its label, hint, error and checks and
hands them, with `aria-invalid` while there is an error, through a context that `Input`,
`Textarea` and `SelectTrigger` read (`useFieldControl`), so tabbing back to a field reads its hint
or its error and the red edge follows the error without the caller setting `aria-invalid`. Give
`htmlFor` only when the control has an id of its own; one control per `Field`. The error line
carries a warning glyph, so it is not told by colour alone.
The databases section's dialogs had assembled their own forms out of `Label`, `Input` and a
`space-y-1.5` div and arrived at three label sizes, two input heights and no way to write an error
beside the field that caused it. They are built from these now, and the statement in one is the
server's own: a structure change (`components/database/schema/change-dialog.tsx`) asks its route with
`?preview=1` as the reader types and draws the answer, the command stays off until the server has
planned exactly what the form says, and the run sends the same body. A change that rewrites or may
refuse rows adds a warning `Notice` under the statement and asks its second question in the dialog's
own footer rather than in a second dialog, because the subject and the statement it is about are
already above it; a change that destroys goes through `useConfirm` with the subject named and the
statement from one preview. A row edit is reviewed the same way (`kit/sql-review.tsx`): the
statements a change set will run are the server's dry run of it.

**A page that is a form stacks each section's head over its fields, in one column.** `FormSection
aside`, run in `FormSections`, sets the title over the fields with the section's current state under
it as data — the address it answers at, the certificate on disk — and holds the whole section to
`max-w-3xl`, the 48rem the fields already had. `FormSections` keeps to the same width, so the
hairline between two sections, and between two of a settings page's forms, stops where the fields
stop. The heads used to sit in a rail, a 15rem column beside the fields from `lg` (from `xl` inside
a project's settings), made for `/dashboard/configuration`, whose five sections with their heads
over their fields had read as one long run in which a head was as far from the fields above it as
from its own. The rail spent a third of the width on a few words a section and set each head on the
line of the first field label beside it, so a section read as two labels side by side. `aside`
keeps its name from that column, and now means a section of the page rather than of a dialog.

**Where the column sits is the page's call.** A page that is only a form centres it: the nine
project settings pages (`SettingsPage` centres its strip and readings with it, so they line up with
the fields), a game server's settings, the account's Security and each of a database's Settings pages. A 48rem
column against the left edge of a wide page left the rest of it an empty band nothing explained. A
form that is one block among full-width ones keeps their left edge instead — the proxy pages'
password files, access lists, backups and watched domains, Security's findings and SSH — because
centred there its title stood 180px in from every title above it. Configuration's settings were the
exception from 0.7.1, two columns of `FormSection aside`s (`max-w-none`) with the certificate across
both, so that five short sections did not leave two thirds of a wide screen empty; SSH's four
directive groups took the same two columns then. In 0.8.7 Configuration went back to the one centred
column, with the account's Profile, Security and Workspace (`components/settings/settings-column.tsx`:
`SettingsColumn` around `SettingSection`s, the settings pages' floating bar under them): two
unrelated heads on one line read as a pair, and the operator named a project's settings as the shape
every settings page should have. What made the single column too tall was the sentence under every
field and switch, and those are behind each name's ⓘ now (`SwitchRow` is a switch as one full-width
line).

**Spacing does what the rail was for, and type keeps the ranks apart.** The space is asymmetric on
purpose: 32px above and below every section, so 64px and a hairline between one section and the
next, against 16px from a head to its own fields, which stand 20px apart — a head is always nearer
its own fields than the ones above it. The title is the section head, `PanelHeader` plain at
15/600, an h2 where a dialog's is an h3, because it names a part of the page (§8); it draws no rule
of its own, because `FormSections` draws the one between sections. The state under it (the
deprecated `hint`) is `text-xs` muted, 12, between the title and the 11px hints in the fields,
because it is data rather than a caption (§5). `actions` sit at the far end of the head's row, and so does a settings
section's `settingStatus`, so "Unsaved changes", arriving with the first keystroke, takes no line of
its own and pushes no field down.

**A strip at the head of a settings column arrives for what is stored, never for what is typed.**
A database's settings draw `PendingStrip` for the one thing a server holds that is not in force: a
value it reads only when it starts (`database/ops/settings-pending.tsx`). It names each, links it to
where it is changed, and carries Restart, because here no header owns that command. Unsaved edits
are named on it while it is drawn and are otherwise the save bar's and the heads' alone: a strip
that arrived with the first keystroke would push the field being typed in down a line, which is what
`settingStatus` at a head's far end exists to avoid. A change that replaces a server's container is
a field like any other and is saved by the bar, which asks first (`database/ops/settings-ask.tsx`):
Save waits on the confirmation, and a cancelled or refused change leaves the draft as it was chosen.

**The deployment settings are that shape, and they were the last pages in the product that were
all containers.** Each of the nine was a stack of framed cards — a title strip, the form, a footer
holding Save — on the argument that things filled in one at a time read best as bordered boxes,
which is the wrong shape for a page that *is* a form. `components/settings/setting-card.tsx` and
`settings-page.tsx` draw them now. `SettingsPage` reads the configuration, then, in the same
`SettingsColumn` the account and Configuration pages use, draws what is saved but not live yet (a strip with no
button: the project context row already carries the one "Deploy changes", and a second brand face a
hundred and fifty pixels under it was two commands on one surface; `PendingStrip` in
`components/settings/pending-strip.tsx` draws it from a headline and a list of items — a mark, a name, a word for
what happened to it, where it is changed — and `pending-changes.tsx` feeds it the deployment's, so any
settings form can say what it holds that is not in force yet), the page's readings, and its
forms in one run of sections. `SettingForm` is one `<form>` and one write, and may span several
sections, because what one PUT writes is one form — Runtime is five. `SettingSection`
is a section's head carrying what the section currently is as data (the host and branch it builds
from, the port it answers on) and, at its far end, at most one `settingStatus`: *Not saved*,
*Unsaved changes* or *Saved · not live yet*. A form draws no Save of its own. Each one ended on
its own button at the fields' right edge, so a page of three forms carried three Saves in three
places and none of them was where the eye was after an edit two screens up. The page has one Save
instead (`components/settings/save-bar.tsx`): `SettingsPage` mounts a `SaveBarProvider`, every `SettingForm`
puts itself on it with its dirty state, its count and its `onSave` (which resolves true when the
write went through), and while any form holds an edit a bar floats at the foot of the content area,
centred on the column it saves. It is the one thing on a settings page that floats, so it takes a
popover's surface and shadow (§2), and it rises in (§11). It counts the edits across the page,
names the parts they are in and when they apply, and carries Discard and Save; ⌘S (Ctrl+S) and
Enter in a field are the same Save. Save writes each dirty form in page order, one after another,
because two forms on one page write the same configuration: `useConfiguration.save` sends the
revision the last write handed back and fills what a form does not own from that copy, and a form
that owns only a field of a part (Build's release tasks are a field of `build`) passes a function
of the latest copy. A refused form keeps its edits and says why under the field, and the bar stays
with what is left; when everything went through it says *Saved* and when it applies, then leaves —
so the forms no longer toast their own success. What the bar says is one rule
(`components/settings/save-bar-words.ts`): a form's `applies` is one of four kinds (at once, when nginx next
reloads, when the server restarts, on the next deployment), a page holding several is told which is
soonest and what the rest wait for, a form whose save does something none of them says gives the
line and the command in its own words (`note`, `verb`; the note is also where it says why it cannot
be saved), and a form without one gets the bar's words for a value that cannot be saved. A column beside a rail asks for the bar `inline`, at the foot of that column. A proxy
site's form keeps a bar of its own, since it is one draft with two saves. A clean page has nothing to save and no button:
re-checking an untouched Source means saving an edit to it. The game server's settings take the
same bar, and hold its Save while a value is outside its declared range; so does Account → Workspace,
whose purposes and rail switches are one draft of the sidebar, and the profile, whose display name
and sign-in name are a draft beside the rail's card drawn from it (the picture still applies the
moment it is chosen). Each form keeps its draft keyed on
a digest of its own saved value (`useSettingDraft`): every save writes a new revision of the whole
configuration, and a draft keyed on the revision was thrown away by a save of the section beside it.

**A setting on a reading page takes the same bar.** Routing's forwarding is two switches on a
page that otherwise reports, and stood mid-page as two half-width blocks with a sentence and
footnotes each. It is a view of that page now (`network/routing/forwarding.tsx`): one centred
`SettingSection` holding a `WireFlow` of what the setting carries and for whom, then a switch row a
family on hairlines, in a `SettingForm` on a `SaveBarProvider` the page mounts itself, with
`applies="immediately"`. A switch pressed is a draft; Save turns a family on at once and asks
before it turns one off, the bar waiting on the answer (`onSave` resolves when the confirmation
closes). What was a footnote (how Docker was counted, the kernel's forwarded count, why a switch is
locked) is behind the `ⓘ` beside the row's name, and an account that cannot change the setting
reads *On* or *Off* in the switch's place.

**A switch says one clause; the reasoning is behind a mark on the section's facts line.** The
switches on Runtime, Build and Domains each carried three or four lines under their name (what
traffic replay sends and withholds, what sleeping keeps, how a rehearsal restores its copy), read
again on every visit. An `OptionRow`'s hint is one clause now, the section's facts line still says
what the section currently is, and the paragraph a reader wants once is an `Explain` at the end of
that line (`components/settings/facts-line.tsx`, `FactsLine`; `components/explain.tsx`, which a
hover, a keyboard's focus or a press opens, and a tap on a phone): there rather than on a field the switch reveals,
because those fields are not on the page while the switch is off, which is when the question is
asked. **A list inside a settings form is one line per thing, and the line opens its small form.**
Health checks and release tasks were framed boxes of fields, open all the time, so two of either
made the foot of the page a stack of forms. Each is a `ChoiceRow` now (`settings/health-checks.tsx`,
`settings/release-tasks.tsx`): what it is drawn as, its name over what it does in mono, its
readings in columns (how long a check keeps trying and whether it is required; how a task last
went and its timeout), and the row opens a `SidePanel` holding the same labelled fields. The sheet
edits the page's draft and has no Save of its own — its foot is Remove, *Saved with the page* and
Done — so what is saved, and when, is still the page's one bar. A refusal that names the row takes
the line its summary was on, so a refused row is the height of every other. **A game server's
settings file is the same page** (`game/settings.tsx`): `SettingsPage` with its pending strip, the
blueprint's declared keys under World, Players and Network (`game/settings-model.ts` sorts them and
writes each head's facts; a key it does not know lands in a last section), `Segments` for a closed
choice, the file's own key on the label's hover and in the folded file view, what a changed field
was at the far end of its own label line so an edit moves nothing, and *Applies when the server
restarts* on the save bar (`SettingApplies` `"restart"`).

**A settings column beside a rail keeps the bar at its own foot.** The bar is centred on the content
area, which is the column's centre only while the column is alone in it. An account of a database
(`database/ops/access-account.tsx`, `access-redis-account.tsx`) has its settings as one view of a
page that stands beside a rail of its siblings, so its `SaveBarProvider` is `inline`: the same bar,
held to the foot of the column it saves rather than floated over the area. The grants staged on the
same page wait in a bar of the same surface at the foot of their own view, ending in *Review…*
rather than *Save*, because what it sends is read as the server's statements first.

**A database's Settings are that shape too, as pages.** They were one page of six sections under a
sticky strip of anchors, and the section a reader most often came for — the danger zone, one
parameter — was the one at the foot of six screens. Since the 2026-10-16 pass each is a page of its
own, listed under a *Settings* group in the database's rail the way a project's are
(`database/shell/settings-pages.ts` beside `PROJECT_SETTINGS_NAV`): General, Network, Resources,
Parameters, Extensions (a key–value server's *Modules*), Version and Danger zone, each
listed only where the engine has it, Version only for an administrator and the danger zone only for a
role that may destroy. What else
the server holds is not among them: it changes nothing about the connection, and Home's *Databases on
this server* already lists it with Connect and New database, so the old `#databases` anchor opens Home. Every page
is one centred column of `SettingSection`s under the page's one save bar, and the labels and the
protection switch are one `SettingForm` over two sections, saved there rather than by a button of
their own. Two of them open on a `SettingPicture` on the page's ground, as the section's other
pictures stand: General draws the address as the way the dashboard reaches it
(this server, the account it signs in as, the server as its product, the database it opens), Network
who can reach the port (this machine's programs and *Anywhere*, through the container as Docker's
whale, to the server; amber while it is published on every interface, dashed while nothing outside
reaches it). Access opens on who signs in, each account
wired to the server and pulsing while it holds sessions, and Backups on where the dumps go — the
database and its schedule, this server's dump tool, the directory — the line in the colour of how
recent the newest dump is. An address from the one-page Settings (`#danger`, `#reachability`, a
parameter search `?q=`) is sent on to the page that holds it.

**A sheet is the same anatomy wherever it opens**, which is `SidePanel`'s doc comment and `Modal`'s
too. The deployment section's sheets each put a different body in the one frame — a `Select` for a
kind, an eyebrow `<legend>` over a group, Cancel on one and not the next — so a sheet was
recognisable by which page opened it. A sheet that edits is titled `Edit {name}`, one that adds
`Add {kind}`, and a read-only history sheet `{name} · history`, waiting on the skeleton of its
list. In order: a sheet that edits a thing that exists opens on
that thing, its mark, its name and its `FormFacts`, so the subject is recognised before a field is
read; a choice between kinds — a credential kind, a channel's service, a webhook's provider, an
alert's measure, a schedule's action — is a `ChoiceGrid` of cards carrying their marks (a
service's own logo where it has one), never a `Select`, because a kind is picked by its mark;
groups of fields are `FormSection`s, a title and a hairline, never a legend quieter than the labels
it heads (§8); options are an `OptionList`; and the footer is Cancel, then the one command,
right-aligned, with a line about what the command will do first and pushed to the left. A body that brings its own command — the traffic-alert form,
which opens both on Automation and on Logs — hands it to the sheet's footer through
`SidePanelFooter` rather than drawing a second foot inside the body.

**A field and the controls that belong to it are one box.** `ui/input-group.tsx` — shadcn's
`InputGroup`, re-sized onto this product's control ladder — holds an `InputGroupInput` with
`InputGroupAddon`s at either end, separated from the field by a hairline rather than by a gap. It
exists because of the shape the deployment forms had in four places: an `Input`, a 14px `Switch` and
a 36px `Button` standing in one wrapping flex, so a single decision — *this host, over HTTPS, check
it* — was three boxes at three heights, and the smallest of them was the one that decides whether
the site answers on 443. Inside a group there is one border, one height and one focus ring: the
group takes `focus-ring-within` and the field drops its own, because the border it would hang a ring
on now belongs to the group. A binary inside a group is a `Toggle` the height of the field (pressed
is `bg-accent` and a `text-brand` mark, which is §3's selection), never a `Switch` — a switch is the
control for an option in a list of options, where a sentence names it and it answers. That toggle is
`InputGroupToggle`, in the same file: it began as `/deploy/new`'s HTTPS segment, and the settings
pages wanted the same control for two more fields — *Read only* on a container path, *Reference* on
a variable's value — and the credential and channel sheets and first sign-in a third, *Show* on a
secret. Its word goes on a phone, where 390px of
field had become 140px of field and 250px of labels, and its `aria-label` says the whole of what it
does ("Serve this hostname over HTTPS") at every width. The requests page's path filter is the
reference for a field that holds its own binary: *Pages only* inside the path's group, and the
narrowings already applied beside it as chips that each clear themselves. A group sits
*inside* a `Field` and takes its label, its hint and its error; it does not replace one.
**Every rule inside a group is the addon's**, drawn for each child after the first — a call site
adding its own `border-l` to the button it puts there gets two pixels where §2 asked for one, which
is what the environment rows shipped with while the hostname field a section above them drew the
same divider correctly.

**A closed set of short words is one segmented control.** `components/segments.tsx` is a single
`ToggleGroup` for two to five words — a Python release, the stage a variable reaches, a health
check's kind and phase, an HTTP method — which the settings had drawn as a `Select` (a closed set
asked for as if it were open, behind a press that hid the other answers) or as free text that
accepted "3.9". The segments not chosen step back to the muted ink, because `bg-accent` on its own
is one step of ground the reader has to compare against its neighbours rather than a mark; and an
empty value is ignored, because Radix lets a second press clear the choice and here there is always
an answer. Two binaries that belong to one row — a domain's HTTPS and its password, a variable's
Secret or Config — are one outline `ToggleGroup`, not two switches.

**A value the operator stores is not a password field.** Chrome ignores `autocomplete="off"` on a form
with a `type="password"` field: it offers saved sign-ins there and reads the text field before it as
the username. The three variable editors take `secretValueField` and `NOT_A_SIGN_IN` from
`lib/secret-field.ts`, a text field drawn as dots with `-webkit-text-security`, and it stays a text
field while masked because Chrome remembers a field that has ever been a password one.

**A rule a value has to meet is lit as it is met.** `FieldCheck` (`form.tsx`) sits under the field
it belongs to — a credential's name rules, a Telegram channel's token and chat id, a password's
length and mix — so an error is
never the first the reader hears of a rule; a run of them is a `FieldChecks`, the polite live
region (never `role="alert"`, because a rule being met is news rather than an interruption), and
each check says "met" or "not yet" to a screen reader.

**An explanation one step from a thing is an `Explain`** (`components/explain.tsx`): an ⓘ after a
label or a fact (`face="icon"`, the default; `Field`'s `info` draws one), a dotted term inside a
sentence (`face="term"`), or the consequence behind a control that already does something
(`face="wrap"`). A resting mouse opens it and can move onto it, the keyboard's focus opens it, and
a press, a tap or Enter pins it until Escape or a press outside, so it opens on a phone. Its press
box is 24px, 44 on a touch screen, without the glyph growing. A `Tooltip` is only the name of an
icon control, and a `HoverCard` only a decorative preview: neither opens for a finger.

**A run of filter chips is a `ChipStrip`** (`chips.tsx`). On a phone it scrolls sideways and bleeds
to the page's gutter, so the chip cut off at the edge is the cue that there are more; wrapped, a
strip of five broke into ragged lines with the last chip alone and pushed the list it filters a
screen down. From `sm` it wraps as chips always have. **A chip has an edge at rest** — `--input`, the
outline every field and outline button beside it has — and chosen, the accent ground under
`--border-strong`. Bare text until chosen, a chip in the log pane's filter row beside bordered fields
read as a caption: "Errors 238" was a word with a number, not a thing to press. A chip that is a legend — the request log's
status families — may colour its count for the chosen state, so "5xx 13" says which thirteen.

**A confirmation names its subject.** `ConfirmRequest.subject` draws the thing an act touches above
its sentence — its mark, its name and the facts that tell it from its neighbours — because "Remove
credential" over GitHub's logo and *GitHub PAT · github.com* is recognised before it is read, which
is the moment the wrong one gets caught. Where the act takes a typed phrase, the phrase is a
`Field` like every other in the product rather than a bare label at a fourth size, an `InputGroup`
whose mark at the end answers the typing: the phrase is right, and the button under it is live.

**A fold is a section whose head is a button.** `Disclosure` is a native `<details>`, so a page can
open one from outside by setting `.open` (which is how a preflight finding reaches a control folded
away inside one) and find-in-page can reveal it; the platform triangle is replaced by the chevron
the rest of the product draws. It exists because one screen had four of these in three spellings:
two bare `<summary>`s at different sizes, and — on Configure's Advanced — a full-width framed strip
in `bg-surface-header`, a ground this file reserves for a `Pane`'s chrome, carrying a glyph §14 does
not allow a header and the word "Show" at the far end. It was the only block on that screen still
drawing a box around itself.

Unifying those three left the opposite defect: a left chevron in front of a 13px word, in a column
of sections that each began with a title and a rule, so the two largest parts of Configure — the
build settings and Advanced — read as stray lines of text. Giving it a section's anatomy instead —
the title at a section's rank, a chevron at the far end — fixed the rank and not the affordance, and
made the affordance worse: the fold was then *indistinguishable* from the `FormSection` heads above
and below it, and the only thing saying otherwise was a 16px chevron a thousand pixels from the word
it belonged to. Nothing on it said "press me" until the pointer was already on it, which is no use,
because the reader has to decide to go there first.

**So a section fold is drawn as what it is: a wide button.** `--control` over `--input` at
`rounded-lg`, which is the resting anatomy of every other control on the screen — the outline
button, the select, the field — with the chevron anchored to that edge and the body below it needing
no rule, because the head has its own. §2 allows a control a face; what it refuses is a block
pretending to be one, and the framed strip this descends from was refused for its ground
(`--surface-header`, a `Pane`'s chrome), a glyph §14 bans and the word "Show", never for having an
edge. `quiet` is the other rank, for a fold *inside* a section — a pasted block, a clone option, an
effective plan: no face, a hover wash, and its chevron **leading**, because at the far end of a line
of body text a marker reads as unrelated punctuation.

`facts` says what is inside while it is shut, because "Advanced" tells the reader nothing about
whether their answer is in there and the cost of finding out is a press and a wall of fields. It is
drawn in the `<summary>`, which is the one place content survives the fold being closed, so it joins
the control's accessible name — and that is the right name. It takes a third of the row and no more,
because the title's `flex-1` gives it a flex base of zero and a facts line sized by its own content
would otherwise take the width it wanted and truncate the title instead. It yields the row entirely
below `sm`, which is the one thing to know when choosing what to put in it: **`facts` is what the
fold holds, never a caveat the reader needs whether or not they open it.** "Clone options · apply to
every import here" keeps its qualifier in the title for exactly that reason — the switches inside
govern the rows above the fold, so a phone that never sees the second half of the sentence is a
phone that cloned without submodules and was not told.

The summary carries `role="button"` and `aria-expanded`: a native `<summary>` is a disclosure widget
the platform knows about but exposes no readable expanded state, and the framed strip this replaced
was a real `<button aria-expanded>` that did. `aria-expanded` is seeded from `open` and thereafter
fed by the element's own `toggle` event rather than derived from the prop — a caller may pass `open`
without `onOpenChange` (the build fold computes its initial state from detection and has nowhere to
store the operator's later answer), and React writes the `open` property only when the prop changes,
so a prop-derived value went on announcing "collapsed" over fields that were plainly on screen. The
one thing `role="button"` costs is the heading outline: a button's descendants are presentational,
so a fold cannot also be a heading the way a `FormSection` is (an `<h2>`), and the standard
`<h3><button aria-expanded>` shape is not available inside `<details>`, whose first child must be
the `<summary>`. That is the same as it was before the unification — the framed strip was not a
heading either — and the widget semantics are worth more here than the outline entry. A caller that
has to open one from outside passes `open`/`onOpenChange`; the rest pass neither.

**An option is one control, not a sentence with a switch at the end of it.** Three things had come
apart in `OptionRow`. Its switch was `size="sm"` — 14px, shorter than the 13px line of text naming
it, and the only place in the product drawing that size while every other switch was the default 18.
Its text ran the full measure of a 1200px panel, so the switch was not at the end of a sentence but
across a void from one. And what the switch *revealed* was a sibling of it at the same rank: on
Configure, "Hostname" sat directly under "Publish on a public hostname" in the same 13px medium,
with nothing saying the field existed *because* the switch was on. It is now the product's own
switch, a label row that washes under the pointer so both ends light as one target, a measure that
stops where a line stops being readable, and children indented behind a rule — the containment §7
gives a `Group`, drawn as one line rather than four sides because this fence already has a head.
The switch is centred on the row rather than hung off the title's line: a two-line option put it
against the top edge of a box it was nowhere near the middle of, and it read as having slipped.

**And on `/deploy/new`, an option's title says the whole thing.** §5 took the sentence out from under
every page, panel and chart title in the product and left it under a switch, one rung smaller — so a
flow screen of six options was six titles and six captions, and the caption is what made the row two
lines tall in the first place. "Report each release on the commit" plus "Posts a pending, success or
failure status to GitHub through the dashboard's own credential" is one fact written twice; "Report
each release as a commit status" is the fact (Settings, which knows the host, names it: "…as a GitLab
commit status"). Where the hint carried a *consequence* the title
has to carry it too — "Required — a failing readiness check blocks activation", not "Readiness check
required" — and where it carried something that belongs to the whole section rather than to one
switch (which branch is watched, that nothing fires before the first deployment) it moves to the
section's `FormNote`, where it is said once. Configure's container access is the shape to copy:
"Run privileged — every host device and kernel capability", "Share the host's network — every port
it opens is open on the host", "Build without the cache — every layer from scratch". A risky
option's title turns amber only while it is on — a reading of state, not a warning at rest — and a
template's yes-or-no input is an `OptionRow` whose label is the title, never a switch under a
label with an *On*/*Off* word beside it.

`hint` stays on the component, and §5's second exception is why: the ambiguous-candidate rows on
Configure pass `high confidence · Next.js via recipe in apps/web`, which is **data** about the option
rather than prose about the switch, and data stays. Roughly forty-five option rows elsewhere in the
product still pass prose; they are not wrong so much as not yet done.

# Settings page

Settings in one centred column under one floating save bar (D8). Reference:
`/deploy/[id]/settings/general`.

**Reference:** `components/deploy/settings/general.tsx` on `SettingsPage`
(`components/settings/settings-page.tsx`). A sibling's shot is
[`docs/readme/deployment-domains.png`](../../../readme/deployment-domains.png). Its siblings are a
project's other settings pages, a game server's settings, a database's Settings pages, the
dashboard's Configuration, and the account's Profile, Security and Workspace.

**Components:** `SettingsColumn`, `SwitchRows`, `SwitchRow` (`components/settings/settings-column.tsx`);
`SettingForm`, `SettingSection`, `settingStatus` (`components/settings/setting-card.tsx`);
`SaveBarProvider` (`components/settings/save-bar.tsx`); `PendingStrip`; `DangerRows` and
`DangerRow`; `Field`, `FieldRow`, `Segments`, `ListField`, `SecretField`, `FactsLine`,
`SettingPicture`. All of them are in [`../components/forms.md`](../components/forms.md), and the
rule is [`../patterns/settings-save-and-pending.md`](../patterns/settings-save-and-pending.md).

## The regions, in order

1. **The page's own header**: the header of the thing whose settings these are (a project's, a
   database's), or the section's identity line. A settings page adds no title of its own under
   it: the first section's head names what the page is.
2. **The strip**, only when something is stored and not in force: `PendingStrip` at the head of
   the column.
3. **The sections**, in one centred column at the fields' width: each a head (title, what the
   section is now as data, one status at the far end, a small outline action) over its fields. A
   section with a shape opens on a `SettingPicture`; one with facts on a `FactsLine`.
4. **The danger zone**, last: `DangerRows`, the page's only framed block.
5. **The save bar**, floating at the foot while any form holds an edit.

## Skeleton

```tsx
export function ThingSettings() {
  const saved = useThingSettings() // the read, and `save`
  const draft = useDraft(saved.data)
  return (
    <Page>
      <PageContext title="Thing settings" />
      <SaveBarProvider>
        <SettingsColumn strip={<ThingPending pending={saved.data?.pending} />}>
          <SettingForm
            name="Address"
            canEdit={saved.canEdit}
            dirty={draft.dirty}
            changes={draft.changes}
            applies="immediately"
            onSave={() => saved.save(draft.value)}
            onDiscard={draft.reset}
          >
            <SettingSection title="Address" state={<AddressNow value={saved.data} />} status={settingStatus({ dirty: draft.dirty })}>
              <Field label="Public address">
                <Input value={draft.value.address} onChange={(e) => draft.set({ address: e.target.value })} />
              </Field>
              <SwitchRows>
                <SwitchRow title="Redirect to HTTPS" info={WHY_REDIRECT} checked={draft.value.redirect} onCheckedChange={(redirect) => draft.set({ redirect })} />
              </SwitchRows>
            </SettingSection>
          </SettingForm>
          <SettingSection title="Danger zone" tone="danger">
            <DangerRows>
              <DangerRow title="Delete thing" sentence="Removes it and its data. Backups are kept." action={<DeleteThing />} />
            </DangerRows>
          </SettingSection>
        </SettingsColumn>
      </SaveBarProvider>
    </Page>
  )
}
```

A project's settings come through `SettingsPage`, which reads the configuration and mounts the
provider and the column itself; a new settings page outside a project writes them as above. The
draft is the page's own hook: it holds what is typed against what is saved, counts the changes and
resets.

## The four states

| State | What the page draws |
| --- | --- |
| Loading | The column at its width with each section's head and bones for its fields, so the first field is where it will be. The save bar is not drawn. |
| Empty | A section with nothing in it yet (no domains, no variables) keeps its head, and its body says so in one line with the action that adds the first (`EmptyNote`, or the head's outline button). |
| Unavailable | An account that may not change the settings reads each value in its control's place and never sees the bar; a section it may not see at all is `NeedsAdmin`. A setting whose tool is missing is `MissingThing size="block"` in its section. |
| Failed | A failed read is `ErrorState` in the column, with Try again. A refused save keeps the draft, marks the field the server named and says the server's words over the form. |

## The phone

The column is the screen's width, and every row is one line: a `SwitchRow`'s reasoning is behind
its ⓘ, which a tap opens. The strip's items fold under its headline. The save bar stands above a
pane bar where the page has one. A field keeps 16px text.

## Checklist

- [ ] One column, centred, drawn in `SettingsColumn`. No cards side by side.
- [ ] One Save: the floating bar. No form ends on its own button, and nothing saves by itself.
- [ ] Every form has a `name`, says when its edits apply (`applies`), and resolves `onSave` true
      or false.
- [ ] A status arrives at a head's far end, never under it. The strip is only for what is stored
      and not in force.
- [ ] No paragraph under a title or a field: a facts line, an `Explain`, a `SwitchRow`'s ⓘ.
- [ ] A small closed choice is `Segments`; a secret is a `SecretField`; a list of entries is a
      `ListField`.
- [ ] Destructive acts are in the danger zone at the foot, each with its sentence and one button,
      and each asks ([`../patterns/confirmations.md`](../patterns/confirmations.md)).
- [ ] Sections are `h2`, danger rows `h3`.
- [ ] A reader without the right to change a setting can still read it.

## The spec to add

1. The column is centred and its sections' heads are the level under the page's title.
2. Typing in a field raises the bar with "1 change", the section's head says *Unsaved changes*,
   and nothing under the field moved.
3. Save sends one write, the bar says *Saved* and leaves; Discard puts the value back.
4. A refused save keeps the draft and shows the server's words.
5. With something stored and not live, the strip names it and links to where it is changed.
6. At 390 the bar clears the foot of the screen and the page does not scroll sideways.

`tests/browser/deploy-settings-a.spec.ts` and `deploy-settings-b.spec.ts` are the models.

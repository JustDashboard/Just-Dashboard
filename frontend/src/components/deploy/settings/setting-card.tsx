"use client"

import { cn } from "@/lib/utils"
import type { DeploymentEnvironmentConfiguration } from "@/lib/types"
import { FormNote, FormSection, FormSections } from "@/components/form"
import { Status } from "@/components/status-dot"
import {
  SaveBarProvider,
  useSaveBarEntry,
  type SettingApplies,
} from "@/components/deploy/settings/save-bar"
import { PendingChanges } from "@/components/deploy/settings/pending-changes"
import { LastFailureRemedy } from "@/components/deploy/settings/last-failure"
import {
  ConfigurationState,
  type useConfiguration,
} from "@/components/deploy/settings/use-configuration"

/**
 * The shape every deployment settings page is drawn in.
 *
 * A settings page used to be a stack of framed cards — a title strip, the
 * form, a footer holding Save — on the argument that a stack of things you
 * fill in one at a time reads best as bordered boxes. It was the last place
 * in the product where the whole page was containers, and it was the wrong
 * shape for a page that *is* a form: §7 sets such a page as one column of
 * sections divided by hairlines, each head over its fields carrying what the
 * section currently is as data, and Configuration and the account's Security
 * page read the same way.
 *
 * The pieces, outermost first:
 *
 *   `SettingsPage` — loads the configuration, then the pending strip and the
 *   page's forms, rising once when the first read lands; and the page's one
 *   Save, which floats at the bottom while anything holds an edit
 *   (`save-bar.tsx`).
 *   `SettingForm` — one form, one write. It may span several sections
 *   (Runtime is five), because what one PUT writes is one form; the bar
 *   saves every dirty form on the page in order.
 *   `SettingSection` — one head and its fields.
 */

/**
 * The sections' centred column, for the page around them and for what sits
 * under a form rather than in one of its sections.
 */
const FIELDS_COLUMN = "mx-auto w-full max-w-3xl"

/**
 * A settings page: its configuration read, what is saved but not live, and
 * its forms in one run of sections — all of it in the one centred column, so
 * the strip lines up with the fields under it.
 *
 * No heading of its own. The project's other pages add none under the
 * project header, and the first section's head already names what the page is.
 * The content rises once when the first read lands and not on every revision
 * after it, because a save is not an arrival. The room at the foot is the
 * save bar's, so it never lies over the last field on the page.
 */
export function SettingsPage({
  state,
  pageKinds,
  children,
}: {
  state: ReturnType<typeof useConfiguration>
  /** The pending-change kinds this page edits — see `PendingChanges`. */
  pageKinds?: string[]
  children: (configuration: DeploymentEnvironmentConfiguration) => React.ReactNode
}) {
  return (
    <SaveBarProvider>
      <ConfigurationState state={state}>
        {(configuration) => (
          <div className={cn("min-w-0 animate-rise space-y-8 pb-20", FIELDS_COLUMN)}>
            <PendingChanges pending={configuration.pending} pageKinds={pageKinds} />
            <LastFailureRemedy />
            <FormSections>{children(configuration)}</FormSections>
          </div>
        )}
      </ConfigurationState>
    </SaveBarProvider>
  )
}

/**
 * One form of a settings page: its sections, and the refusal when the server
 * turned the save down. It draws no Save of its own — it puts itself on the
 * page's save bar, which counts its edits beside the rest of the page's and
 * calls `onSave` when Save is pressed. Enter in a field submits the form,
 * which is the bar's Save too, so one key never saves half a page.
 *
 * `name` is the form's accessible name — the thing a reader, an assistive
 * technology and a test all find it by — and the word the bar uses for the
 * part of the page the edits are in. `dirty` and `changes` come from
 * `useSettingDraft`. `onSave` resolves true when the write went through and
 * false when it was refused, which is how the bar knows to say *Saved*.
 * `note` is a line about this form at its foot — why it cannot be edited.
 */
export function SettingForm({
  name,
  onSave,
  dirty = false,
  changes = 0,
  saving = false,
  invalid = false,
  canEdit,
  onDiscard,
  applies = "next-deployment",
  note,
  error,
  children,
}: {
  name: string
  onSave: () => Promise<boolean>
  dirty?: boolean
  changes?: number
  saving?: boolean
  /** Holds the bar's Save while a field is out of range. */
  invalid?: boolean
  canEdit: boolean
  onDiscard?: () => void
  applies?: SettingApplies
  note?: React.ReactNode
  /** A refusal that belongs to the whole form rather than to one field. */
  error?: React.ReactNode
  children: React.ReactNode
}) {
  const saveAll = useSaveBarEntry(
    canEdit,
    { name, dirty, changes, saving, invalid, applies },
    { save: onSave, discard: onDiscard },
  )
  return (
    <form
      aria-label={name}
      onSubmit={(event) => {
        event.preventDefault()
        void saveAll?.()
      }}
      className="min-w-0 py-8 first:pt-0 last:pb-0"
    >
      <FormSections>{children}</FormSections>
      {error && (
        <FormNote tone="danger" role="alert" className={cn("mt-6 animate-rise", FIELDS_COLUMN)}>
          {error}
        </FormNote>
      )}
      {note && <FormNote className={cn("mt-6", FIELDS_COLUMN)}>{note}</FormNote>}
    </form>
  )
}

/**
 * One section of a settings form: the head, the fields under it.
 *
 * The head carries three things, each optional and each data rather than a
 * caption (§5): under its title `state`, what the section currently is — the
 * host and branch it builds from, the port it answers on — which may hold a
 * product glyph or a branch chip; and at its far end `status`, from
 * `settingStatus`, then `actions`, the small outline buttons that add a row
 * to the section. The status is at the end rather than under the state so
 * that "Unsaved changes" arriving with the first keystroke does not push the
 * field being typed in down a line.
 */
export function SettingSection({
  id,
  title,
  state,
  status,
  actions,
  tone = "default",
  className,
  children,
}: {
  /** A scroll target, for a link from elsewhere into this section. */
  id?: string
  title: React.ReactNode
  state?: React.ReactNode
  status?: React.ReactNode
  actions?: React.ReactNode
  tone?: "default" | "danger"
  className?: string
  children?: React.ReactNode
}) {
  return (
    <FormSection
      aside
      id={id}
      data-slot="setting"
      className={className}
      title={tone === "danger" ? <span className="text-destructive">{title}</span> : title}
      hint={state && <div className="min-w-0 break-words">{state}</div>}
      actions={
        status || actions ? (
          <>
            {status}
            {actions}
          </>
        ) : undefined
      }
    >
      {children}
    </FormSection>
  )
}

/**
 * Where a section stands against what is saved and what is live, as the one
 * `Status` its head carries — or nothing, which is the common case: a
 * "Live" on every head would be noise.
 *
 * `notLive` is for a section that one kind of pending change maps onto
 * exactly — a source change is General's Source, a check change is Runtime's
 * Health checks. A build or runtime plan's digest covers several sections,
 * so those say it once, in the page's strip, rather than guess which head.
 *
 * Drafts outlive a visit to another page, so a reader coming back sees
 * which heads still hold unsaved edits. Each state has its own key, so a
 * status that changes arrives (§11) rather than being repainted in place.
 */
export function settingStatus({
  dirty,
  refused,
  notLive,
}: {
  dirty?: boolean
  refused?: boolean
  notLive?: boolean
}): React.ReactNode {
  if (refused)
    return <Status key="refused" tone="danger" label="Not saved" className="animate-rise" />
  if (dirty)
    return <Status key="dirty" tone="warning" label="Unsaved changes" className="animate-rise" />
  if (notLive)
    return (
      <Status key="not-live" tone="notice" label="Saved · not live yet" className="animate-rise" />
    )
  return null
}

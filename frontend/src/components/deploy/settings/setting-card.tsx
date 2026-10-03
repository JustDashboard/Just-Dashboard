"use client"

import type { FormEvent } from "react"
import { cn } from "@/lib/utils"
import type { DeploymentEnvironmentConfiguration } from "@/lib/types"
import { FormNote, FormSection, FormSections } from "@/components/form"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
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
 *   page's forms, rising once when the first read lands.
 *   `SettingForm` — one form, one save. It may span several sections
 *   (Runtime is five), because what one PUT writes is what one Save means.
 *   `SettingSection` — one head and its fields.
 *   `SettingFoot` — when the change applies, Discard, and Save.
 */

type SettingApplies = "next-deployment" | "immediately"

/**
 * The sections' centred column, for the page around them and for what sits
 * under a form rather than in one of its sections — the game server's foot
 * included, which has no `FormSections` around it — so Save sits under the
 * fields it saves rather than a thousand pixels to their right.
 */
const FIELDS_COLUMN = "mx-auto w-full max-w-3xl"

const APPLIES: Record<SettingApplies, string> = {
  "next-deployment": "Applies on your next deployment",
  immediately: "Applies immediately",
}

/**
 * A settings page: its configuration read, what is saved but not live, and
 * its forms in one run of sections — all of it in the one centred column, so
 * the strip lines up with the fields under it.
 *
 * No heading of its own. The project's other pages add none under the
 * project header, and the first section's head already names what the page is.
 * The content rises once when the first read lands and not on every revision
 * after it, because a save is not an arrival.
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
    <ConfigurationState state={state}>
      {(configuration) => (
        <div className={cn("min-w-0 animate-rise space-y-8", FIELDS_COLUMN)}>
          <PendingChanges pending={configuration.pending} pageKinds={pageKinds} />
          <LastFailureRemedy />
          <FormSections>{children(configuration)}</FormSections>
        </div>
      )}
    </ConfigurationState>
  )
}

/**
 * One form of a settings page: its sections, the refusal when the server
 * turned the save down, and the foot.
 *
 * `name` is the form's accessible name — the thing a reader, an assistive
 * technology and a test all find it by, now that no frame draws its edge.
 * `dirty` and `changes` come from `useSettingDraft`; Save is the brand face
 * only while there is something to save.
 */
export function SettingForm({
  name,
  onSubmit,
  dirty = false,
  changes = 0,
  saving = false,
  canEdit,
  onDiscard,
  applies = "next-deployment",
  note,
  error,
  children,
}: {
  name: string
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  dirty?: boolean
  changes?: number
  saving?: boolean
  canEdit: boolean
  onDiscard?: () => void
  applies?: SettingApplies
  /** In place of the applies line, when the consequence is more particular. */
  note?: React.ReactNode
  /** A refusal that belongs to the whole form rather than to one field. */
  error?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <form aria-label={name} onSubmit={onSubmit} className="min-w-0 py-8 first:pt-0 last:pb-0">
      <FormSections>{children}</FormSections>
      {error && (
        <FormNote tone="danger" role="alert" className={cn("mt-6", FIELDS_COLUMN)}>
          {error}
        </FormNote>
      )}
      <SettingFoot
        applies={applies}
        note={note}
        dirty={dirty}
        changes={changes}
        saving={saving}
        canEdit={canEdit}
        onDiscard={onDiscard}
      />
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

/**
 * The end of a settings form: Save, and — once there is something to save —
 * how many edits it holds and when they take effect.
 *
 * At rest it is Save and nothing else, at the fields' right edge where the
 * switches above it end. Every form used to close on "Applies immediately —
 * no deployment" at the column's far left in 11px grey, a thousand pixels from
 * the button it qualified: on a page of three forms that was three stray lines
 * nobody had asked a question of. When a change applies is news only once
 * there is a change, so it arrives with the count. A `note` is the exception
 * and stays at rest, beside Save, because it says something particular about
 * this form — why it cannot be edited, what a restart still has to do.
 *
 * Save is the outline face while the form is clean and the brand face once
 * it holds an edit — the command face as a function of state (§16), so the
 * one blue on a page of five forms is the form that has something to save.
 * It is never disabled when clean: saving an untouched Source checks it
 * again, which is how an operator finds out a credential stopped working.
 *
 * While dirty the foot follows the reader down the form, as Configuration's
 * apply bar does, because a Save a screen below the field that was changed
 * is how a form gets abandoned half-edited. It is opaque rather than frosted
 * (§16 has no glass), takes its hairline only then, and rises into place
 * rather than snapping (§11 *arrived*). The count is the head's amber
 * `Status` again, because the head has usually scrolled away by the time the
 * bar is what the reader sees. Its row keeps to the fields column. On a phone,
 * while dirty, Discard and Save take half the width each at a thumb's height
 * under the count; clean, Save sits alone at the right edge.
 *
 * `invalid` holds Save while a field is out of range.
 */
export function SettingFoot({
  applies,
  note,
  dirty = false,
  changes = 0,
  saving = false,
  invalid = false,
  canEdit,
  onDiscard,
}: {
  applies?: SettingApplies
  note?: React.ReactNode
  dirty?: boolean
  changes?: number
  saving?: boolean
  invalid?: boolean
  /** Draws Discard and Save; a reader who cannot edit sees only the note. */
  canEdit?: boolean
  onDiscard?: () => void
}) {
  const line = note ?? (dirty && applies ? APPLIES[applies] : undefined)
  if (!canEdit && !line) return null
  const controls = canEdit && (
    <div className={cn("flex shrink-0 items-center gap-2", dirty && "max-sm:w-full")}>
      {dirty && onDiscard && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={saving}
          onClick={onDiscard}
          className="max-sm:h-11 max-sm:flex-1"
        >
          Discard
        </Button>
      )}
      <Button
        type="submit"
        size="sm"
        variant={dirty ? "default" : "outline"}
        pending={saving}
        disabled={invalid}
        className={cn("max-sm:h-11", dirty && "max-sm:flex-1")}
      >
        {saving ? "Saving…" : "Save"}
      </Button>
    </div>
  )
  return (
    <div
      className={cn(
        "mt-6",
        dirty &&
          "sticky bottom-0 z-20 -mx-5 border-t border-hairline bg-background px-5 py-3 md:-mx-8 md:px-8",
      )}
    >
      {/* Keyed on the state, so the bar rises in when the first edit lands and
          the quiet foot rises back once it is saved or discarded. */}
      <div
        key={dirty ? "dirty" : "clean"}
        className={cn(
          "flex min-w-0 animate-rise flex-wrap items-center justify-end gap-x-4 gap-y-2.5",
          FIELDS_COLUMN,
        )}
      >
        {dirty && (
          <Status
            tone="warning"
            label={
              changes > 0
                ? `${changes} unsaved change${changes === 1 ? "" : "s"}`
                : "Unsaved changes"
            }
          />
        )}
        {line && (
          <div
            className={cn(
              "min-w-0 text-xs leading-snug text-muted-foreground",
              // Beside the count while dirty, so the two read as one sentence
              // about the edit; beside Save at rest, so a note is read as
              // belonging to the button it qualifies.
              dirty ? "mr-auto" : "flex-1 text-right",
            )}
          >
            {line}
          </div>
        )}
        {dirty && !line && <span aria-hidden className="mr-auto" />}
        {controls}
      </div>
    </div>
  )
}

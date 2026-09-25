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
 * shape for a page that *is* a form: §7 puts such a page's section heads in
 * a rail, and Configuration and the account's Security page already read
 * that way. So the heads go down the left, each carrying what the section
 * currently is as data, and the fields go down the right — from `xl` rather
 * than `lg`, because beside the project's own navigation a rail at 1024
 * would leave three fields in a row about 130px each.
 *
 * The pieces, outermost first:
 *
 *   `SettingsPage` — loads the configuration, then the pending strip, the
 *   page's readings and its forms, rising once when the first read lands.
 *   `SettingForm` — one form, one save. It may span several rail sections
 *   (Runtime is five), because what one PUT writes is what one Save means.
 *   `SettingSection` — one rail head and its fields.
 *   `SettingFoot` — when the change applies, Discard, and Save.
 */

type SettingApplies = "next-deployment" | "immediately"

/**
 * The fields column of a rail section, for what sits under a form rather
 * than in one of its sections: it starts past the 15rem rail and its 3rem gap
 * from `xl`, and stops where the fields stop, so Save sits under the fields it
 * saves rather than a thousand pixels to their right on a wide screen.
 */
const FIELDS_COLUMN = "max-w-3xl xl:max-w-[66rem] xl:pl-[18rem]"

const APPLIES: Record<SettingApplies, string> = {
  "next-deployment": "Applies on your next deployment",
  immediately: "Applies immediately — no deployment",
}

/**
 * A settings page: its configuration read, what is saved but not live, the
 * page's figures when it has any, and its forms in one run of rail sections.
 *
 * No heading of its own. The project's other pages add none under the
 * project header, and the first rail head already names what the page is.
 * The content rises once when the first read lands and not on every revision
 * after it, because a save is not an arrival.
 */
export function SettingsPage({
  state,
  pageKinds,
  readings,
  children,
}: {
  state: ReturnType<typeof useConfiguration>
  /** The pending-change kinds this page edits — see `PendingChanges`. */
  pageKinds?: string[]
  /** The page's figures, as a `StatGrid dense`. */
  readings?: (configuration: DeploymentEnvironmentConfiguration) => React.ReactNode
  children: (configuration: DeploymentEnvironmentConfiguration) => React.ReactNode
}) {
  return (
    <ConfigurationState state={state} readings={Boolean(readings)}>
      {(configuration) => (
        <div className="min-w-0 animate-rise space-y-8">
          <PendingChanges pending={configuration.pending} pageKinds={pageKinds} />
          <LastFailureRemedy />
          {readings?.(configuration)}
          <FormSections railFrom="xl">{children(configuration)}</FormSections>
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
      <FormSections railFrom="xl">{children}</FormSections>
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
 * One section of a settings form: the head in the rail, the fields beside it.
 *
 * The head carries three things under its title, each optional and each
 * data rather than a caption (§5): `state`, what the section currently is —
 * the host and branch it builds from, the port it answers on — which may
 * hold a product glyph or a branch chip; `status`, from `settingStatus`; and
 * `actions`, the small outline buttons that add a row to the section.
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
      railFrom="xl"
      id={id}
      data-slot="setting"
      className={className}
      title={tone === "danger" ? <span className="text-destructive">{title}</span> : title}
      hint={
        state || status ? (
          <>
            {state && <div className="min-w-0 break-words">{state}</div>}
            {status && <div className="pt-1">{status}</div>}
          </>
        ) : undefined
      }
      actions={actions}
    >
      {children}
    </FormSection>
  )
}

/**
 * Where a section stands against what is saved and what is live, as the one
 * `Status` its rail head carries — or nothing, which is the common case: a
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
 * The end of a settings form: when its change takes effect, and Save.
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
 * (§16 has no glass), and it takes its hairline only then: at rest it is the
 * last line of the form, not a strip of chrome. Its row keeps to the fields
 * column. On a phone it stacks, at a thumb's height: while dirty, Discard and
 * Save take half the width each; clean, Save sits alone at the right edge,
 * because a full-width slab on every form read as the page's main command
 * with nothing to save.
 *
 * The line is `note` when there is one, else what `applies` says; with
 * neither it is empty. `invalid` holds Save while a field is out of range.
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
  /** Draws Discard and Save; a reader who cannot edit sees only the line. */
  canEdit?: boolean
  onDiscard?: () => void
}) {
  const controls = canEdit && (
    <>
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
    </>
  )
  return (
    <div
      className={cn(
        "mt-8",
        dirty &&
          "sticky bottom-0 z-20 -mx-5 border-t border-hairline bg-background px-5 py-3 md:-mx-8 md:px-8",
      )}
    >
      <div
        className={cn(
          "flex min-w-0 flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between",
          FIELDS_COLUMN,
        )}
      >
        <p className="min-w-0 text-hint leading-relaxed text-muted-foreground">
          {dirty && (
            <>
              <span className="font-medium text-foreground">
                {changes > 0
                  ? `${changes} unsaved change${changes === 1 ? "" : "s"}`
                  : "Unsaved changes"}
              </span>
              {" — "}
            </>
          )}
          {note ?? (applies && APPLIES[applies])}
        </p>
        {controls && (
          <div className="flex shrink-0 items-center gap-2 max-sm:w-full max-sm:justify-end">
            {controls}
          </div>
        )}
      </div>
    </div>
  )
}

"use client"

import { useState } from "react"
import { put } from "@/lib/api"
import { parseMaskedColumns } from "@/lib/pull-requests"
import { notify } from "@/lib/toast"
import type { DeploymentDatabaseLink, PreviewBranchMode } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { Field, FieldRow, FormNote } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { Textarea } from "@/components/ui/textarea"
import { Segments } from "@/components/deploy/settings/segments"
import { useSettingDraft } from "@/components/deploy/settings/use-configuration"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"

type BranchDraft = Record<string, { mode: PreviewBranchMode; columns: string }>

function draftOf(links: DeploymentDatabaseLink[]): BranchDraft {
  const draft: BranchDraft = {}
  for (const link of links)
    draft[link.connectionId] = {
      mode: link.previews?.mode ?? "none",
      columns: (link.previews?.maskedColumns ?? []).join("\n"),
    }
  return draft
}

const MODES: { value: PreviewBranchMode; label: string }[] = [
  { value: "none", label: "Nothing" },
  { value: "branch", label: "A branch" },
  { value: "masked", label: "A masked branch" },
]

/** Only PostgreSQL, MySQL and MariaDB are restored into a copy. */
const BRANCHED_DRIVERS = new Set(["postgres", "mysql"])

/**
 * Database branches for pull request previews: what each linked database
 * gives a preview of this environment. Nothing, by default — a preview
 * reaches no database of production's. A branch is the preview's own copy,
 * restored from the database's newest backup (its point-in-time archive or a
 * backup job's dump) onto the preview's own network with a password made for
 * it, so the preview's `${{database.N}}` reads the branch; masked, the named
 * columns are overwritten in their shape before the preview can read them.
 * A preview of a fork never gets one. Saved apart from the plan, it applies
 * to the next preview built.
 */
export function PreviewBranchesForm({
  projectId,
  environmentId,
  links,
  onSaved,
}: {
  projectId: number
  environmentId: number
  links: DeploymentDatabaseLink[]
  onSaved: () => void
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  const saved = draftOf(links)
  const draft = useSettingDraft<BranchDraft>(`deploy.${projectId}.settings.preview-branches`, saved)
  const value = draft.value
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()

  const changed = links.filter((link) => {
    const now = value[link.connectionId]
    const was = saved[link.connectionId]
    return (
      now &&
      was &&
      (now.mode !== was.mode ||
        (now.mode === "masked" &&
          parseMaskedColumns(now.columns).join(",") !== parseMaskedColumns(was.columns).join(",")))
    )
  })
  const missingColumns = links.find(
    (link) =>
      value[link.connectionId]?.mode === "masked" &&
      parseMaskedColumns(value[link.connectionId].columns).length === 0,
  )

  const save = async () => {
    if (missingColumns) {
      setError(`Name the columns to mask in ${missingColumns.name}, as table.column.`)
      return false
    }
    setSaving(true)
    setError(undefined)
    try {
      for (const link of changed) {
        const entry = value[link.connectionId]
        await put(
          `/deploy/${projectId}/environments/${environmentId}/database-links/${link.connectionId}/previews`,
          {
            mode: entry.mode,
            maskedColumns: entry.mode === "masked" ? parseMaskedColumns(entry.columns) : [],
          },
        )
      }
      onSaved()
      return true
    } catch (failure) {
      notify.error("Could not save what previews get", failure)
      setError(failure instanceof Error ? failure.message : String(failure))
      return false
    } finally {
      setSaving(false)
    }
  }

  const given = links.filter((link) => (value[link.connectionId]?.mode ?? "none") !== "none")

  return (
    <SettingForm
      name="Database branches for previews"
      onSave={save}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      invalid={Boolean(missingColumns)}
      canEdit={canEdit}
      applies="next-deployment"
      onDiscard={() => {
        setError(undefined)
        draft.discard()
      }}
      error={error}
    >
      <SettingSection
        id="preview-branches"
        title="Previews get"
        state={
          <span key={given.length} className="block animate-rise">
            {given.length === 0
              ? "No database of this environment's"
              : `A branch of ${given.map((link) => link.name).join(", ")}`}
          </span>
        }
        status={settingStatus({ dirty: draft.dirty, refused: Boolean(error) })}
      >
        <div className="space-y-5">
          {links.map((link) => {
            const entry = value[link.connectionId] ?? { mode: "none", columns: "" }
            const restorable = BRANCHED_DRIVERS.has(link.driver)
            const set = (next: Partial<typeof entry>) =>
              draft.set({ ...value, [link.connectionId]: { ...entry, ...next } })
            return (
              <FieldRow key={link.connectionId}>
                <Field
                  label={
                    <span className="inline-flex items-center gap-1.5">
                      <ProductLogo size="sm" id={link.driver} />
                      {link.name}
                    </span>
                  }
                  htmlFor={`preview-branch-${link.connectionId}`}
                  info="A branch is restored from the database's newest backup into a container on the preview's own network, with a password made for it, and removed with the preview. A preview of a fork never gets one."
                >
                  <div className="space-y-3">
                    <Segments
                      id={`preview-branch-${link.connectionId}`}
                      label={`What previews get of ${link.name}`}
                      value={entry.mode}
                      options={MODES.map((mode) => ({
                        ...mode,
                        disabled: mode.value !== "none" && !restorable,
                      }))}
                      onChange={(mode) => set({ mode })}
                      disabled={!canEdit}
                      fill
                    />
                    {entry.mode === "masked" && (
                      <Textarea
                        aria-label={`Columns of ${link.name} to mask`}
                        className="min-h-20 font-mono"
                        placeholder={"users.email\nusers.phone"}
                        value={entry.columns}
                        readOnly={!canEdit}
                        onChange={(event) => set({ columns: event.target.value })}
                      />
                    )}
                    {entry.mode === "masked" && (
                      <FormNote>
                        One <code className="font-mono">table.column</code> a line. Each value is
                        overwritten in its shape — letters for letters, digits for digits — on the
                        copy before the preview reads it; NULL stays NULL. Text, numbers and UUIDs
                        are masked, and a table needs a primary key.
                      </FormNote>
                    )}
                    {!restorable && (
                      <FormNote>
                        Branches are restored for PostgreSQL, MySQL and MariaDB databases.
                      </FormNote>
                    )}
                  </div>
                </Field>
              </FieldRow>
            )
          })}
        </div>
      </SettingSection>
    </SettingForm>
  )
}

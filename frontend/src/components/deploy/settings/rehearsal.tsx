"use client"

import { useState } from "react"
import { get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DbConnection, DeploymentEnvironmentConfiguration, RehearsalPolicy } from "@/lib/types"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Segments } from "@/components/deploy/settings/segments"
import {
  DEFAULT_REHEARSAL_POLICY,
  engineName,
  rehearsalCandidates,
  rehearsalPolicyLine,
} from "@/components/deploy/rehearsal-model"
import { useSettingDraft } from "@/components/deploy/settings/use-configuration"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"

/** The form's own shape: numbers as typed, so a field can be emptied. */
type RehearsalDraft = {
  enabled: boolean
  connectionId: string
  whenUnavailable: RehearsalPolicy["whenUnavailable"]
  restoreMinutes: string
  maxBackupMb: string
}

function draftOf(policy: RehearsalPolicy): RehearsalDraft {
  return {
    enabled: policy.enabled,
    connectionId: String(policy.connectionId),
    whenUnavailable: policy.whenUnavailable,
    restoreMinutes: String(Math.max(1, Math.round(policy.restoreTimeoutSeconds / 60))),
    maxBackupMb: String(policy.maxBackupMb),
  }
}

/** The bounds the server holds the terms to (`validateRehearsalPolicy`). */
function policyOf(draft: RehearsalDraft): RehearsalPolicy | string {
  const minutes = Number(draft.restoreMinutes)
  const size = Number(draft.maxBackupMb)
  if (!Number.isInteger(minutes) || minutes < 1 || minutes > 360)
    return "The restore limit is between 1 and 360 minutes."
  if (!Number.isInteger(size) || size < 1 || size > 1_048_576)
    return "The size limit is between 1 MB and 1 TB."
  return {
    enabled: draft.enabled,
    connectionId: Number(draft.connectionId) || 0,
    whenUnavailable: draft.whenUnavailable,
    restoreTimeoutSeconds: minutes * 60,
    maxBackupMb: size,
  }
}

/**
 * Migration rehearsal: before the release tasks run against production, the
 * database they migrate is restored into a throwaway copy — from its
 * point-in-time archive where it has one, else from the newest native dump a
 * backup job made — and the tasks run against that first, in the release
 * image, with the database's address pointing at the copy. A task that fails
 * there stops the release before production is touched. The terms are saved
 * apart from the plan, on the page's save bar, and apply from the next
 * deployment.
 */
export function RehearsalForm({
  projectId,
  environmentId,
  configuration,
}: {
  projectId: number
  environmentId: number
  configuration: DeploymentEnvironmentConfiguration
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  const base = `/deploy/${projectId}/environments/${environmentId}`
  const read = usePoll(
    (signal) => get<RehearsalPolicy>(`${base}/rehearsal`, undefined, signal),
    0,
    [base],
  )
  // The saved connections name the databases; an environment's links exist
  // only once a release has joined their networks, which a first rehearsal
  // comes before.
  const connections = usePoll(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    0,
    [],
  )
  const saved = read.data ?? DEFAULT_REHEARSAL_POLICY
  const draft = useSettingDraft<RehearsalDraft>(
    `deploy.${projectId}.settings.rehearsal`,
    draftOf(saved),
  )
  const value = draft.value
  const patch = (next: Partial<RehearsalDraft>) => draft.set({ ...value, ...next })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const parsed = policyOf(value)

  const tasks = configuration.build.releaseTasks ?? []
  const shellTask = tasks.find((task) => task.runner !== "image")
  const candidates = rehearsalCandidates(configuration.variables)
  const nameOf = (id: number) => {
    const connection = connections.data?.find((item) => item.id === id)
    return connection ? `${connection.name} · ${engineName(connection.driver)}` : `Database ${id}`
  }
  const chosen = Number(value.connectionId) || 0
  // A saved choice the variables no longer reach stays listed, so the field
  // shows what the server will refuse.
  const choices = chosen > 0 && !candidates.includes(chosen) ? [...candidates, chosen] : candidates

  const save = async () => {
    if (typeof parsed === "string") {
      setError(parsed)
      return false
    }
    setSaving(true)
    setError(undefined)
    try {
      await put(`${base}/rehearsal`, parsed)
      read.refresh()
      return true
    } catch (failure) {
      notify.error("Could not save the rehearsal terms", failure)
      setError(failure instanceof Error ? failure.message : String(failure))
      return false
    } finally {
      setSaving(false)
    }
  }

  const number = (
    id: string,
    field: "restoreMinutes" | "maxBackupMb",
    unit: string,
    max: number,
  ) => (
    <InputGroup>
      <InputGroupInput
        id={id}
        type="number"
        min={1}
        max={max}
        value={value[field]}
        readOnly={!canEdit}
        className="font-mono"
        onChange={(event) => patch({ [field]: event.target.value })}
      />
      <InputGroupAddon align="inline-end">
        <InputGroupText>{unit}</InputGroupText>
      </InputGroupAddon>
    </InputGroup>
  )

  return (
    <SettingForm
      name="Migration rehearsal"
      onSave={save}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      invalid={typeof parsed === "string"}
      canEdit={canEdit}
      onDiscard={() => {
        setError(undefined)
        draft.discard()
      }}
      error={error}
    >
      <SettingSection
        id="rehearsal"
        title="Migration rehearsal"
        state={
          <span
            key={typeof parsed === "string" ? "invalid" : rehearsalPolicyLine(parsed)}
            className="block animate-rise"
          >
            {typeof parsed === "string" ? parsed : rehearsalPolicyLine(parsed)}
          </span>
        }
        status={settingStatus({ dirty: draft.dirty, refused: Boolean(error) })}
      >
        <OptionList>
          <OptionRow
            title="Rehearse release tasks on a copy of the database"
            hint="Before they run against production, the release tasks run against a copy restored from the database's newest backup — its point-in-time archive where it has one, else the newest dump a backup job made — on a network of its own with nothing published. A task that fails there stops the release, and production is never touched."
            checked={value.enabled}
            onCheckedChange={(enabled) => patch({ enabled })}
            disabled={!canEdit}
          >
            <div className="space-y-4">
              {choices.length > 1 && (
                <FieldRow>
                  <Field
                    label="Database"
                    htmlFor="rehearsal-database"
                    info="The release tasks reach more than one database. The one chosen is restored and every other address is left as it is, so a task should migrate this one."
                    error={
                      chosen === 0 ? "Choose the database the release tasks migrate." : undefined
                    }
                  >
                    <Select
                      value={chosen > 0 ? String(chosen) : undefined}
                      onValueChange={(connectionId) => patch({ connectionId })}
                      disabled={!canEdit}
                    >
                      <SelectTrigger id="rehearsal-database" className="w-full">
                        <SelectValue placeholder="Choose a database" />
                      </SelectTrigger>
                      <SelectContent>
                        {choices.map((id) => (
                          <SelectItem key={id} value={String(id)}>
                            {nameOf(id)}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                </FieldRow>
              )}
              <FieldRow>
                <Field
                  label="Without a backup to restore"
                  htmlFor="rehearsal-unavailable"
                  info="A rehearsal cannot run when the database has no backup, its backup is over the size limit, or its restore takes longer than the restore limit. Stopping keeps the promise that nothing reaches production unrehearsed; continuing records a warning on the run."
                >
                  <Segments
                    id="rehearsal-unavailable"
                    label="Without a backup to restore"
                    value={value.whenUnavailable}
                    options={[
                      { value: "block", label: "Stop the release" },
                      { value: "warn", label: "Continue with a warning" },
                    ]}
                    onChange={(whenUnavailable) => patch({ whenUnavailable })}
                    disabled={!canEdit}
                    fill
                  />
                </Field>
              </FieldRow>
              <FieldRow>
                <Field
                  label="Restore limit"
                  htmlFor="rehearsal-restore"
                  info="How long restoring the copy may take before the rehearsal counts as unavailable. Each release task keeps its own timeout against the copy, the same as against production."
                >
                  {number("rehearsal-restore", "restoreMinutes", "minutes", 360)}
                </Field>
                <Field
                  label="Largest backup restored"
                  htmlFor="rehearsal-size"
                  info="A dump larger than this, or a base backup that unpacks to more, is not restored; the copy needs that much free disk for as long as the rehearsal runs."
                >
                  {number("rehearsal-size", "maxBackupMb", "MB", 1_048_576)}
                </Field>
              </FieldRow>
            </div>
          </OptionRow>
        </OptionList>
        {value.enabled && tasks.length === 0 && (
          <FormNote className="animate-rise">
            There are no release tasks yet, so a deployment has nothing to rehearse and skips the
            step.
          </FormNote>
        )}
        {value.enabled && shellTask && (
          <FormNote tone="warning" className="animate-rise">
            {shellTask.name} runs in the dashboard&apos;s shell, and only a task that runs in the
            release image can be rehearsed, so every deployment would{" "}
            {value.whenUnavailable === "warn" ? "warn that it was not rehearsed" : "be stopped"}.
          </FormNote>
        )}
        {value.enabled && tasks.length > 0 && candidates.length === 0 && (
          <FormNote tone="warning" className="animate-rise">
            No variable the release tasks receive references a database this dashboard manages (such
            as <code className="font-mono">{"${{database.5.url}}"}</code>), so there is nothing to
            restore a copy of.
          </FormNote>
        )}
        {value.enabled && candidates.length === 1 && (
          <FormNote className="animate-rise">
            Rehearses against a copy of {nameOf(candidates[0])}, the database the release tasks
            reach.
          </FormNote>
        )}
      </SettingSection>
    </SettingForm>
  )
}

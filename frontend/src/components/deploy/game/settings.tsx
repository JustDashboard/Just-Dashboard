"use client"

import { useCallback, useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { get, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BlueprintConfigFile, BlueprintProperty, GameProperties } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Disclosure, Field, FieldRow, OptionList, OptionRow } from "@/components/form"
import { LogText } from "@/components/logs/log-text"
import { Well } from "@/components/panel"
import { ErrorState } from "@/components/state"
import { Input } from "@/components/ui/input"
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
import { Skeleton } from "@/components/ui/skeleton"
import { useProject } from "@/components/deploy/project-context"
import { EmptyStep, ProjectEmpty } from "@/components/deploy/project-empty"
import {
  GameIdentity,
  GameIdentitySkeleton,
  useGameOverview,
} from "@/components/deploy/game/identity"
import { groupFacts, groupProperties } from "@/components/deploy/game/settings-model"
import { HoverTip } from "@/components/deploy/settings/hover-tip"
import { Segments } from "@/components/deploy/settings/segments"
import {
  SettingForm,
  SettingSection,
  SettingsPage,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { useConfiguration } from "@/components/deploy/settings/use-configuration"

/**
 * The server's own settings file, as a settings page like the project's
 * others: the strip of what is saved and not live, then the file's declared
 * keys in sections by what they are about — the world, who may join, how the
 * server is found — each head saying what the section currently is, and the
 * file itself, as it sits on disk, folded away in a last section.
 *
 * It was one section of every field, each with its key as a chip and its
 * description under it, closed by a note that the server reads the file on
 * start. The key is the file's word, not the reader's, so it is on the label's
 * hover and in the file view; and when a save applies is the save bar's line
 * to say, where the other settings pages say it.
 *
 * Only the keys the blueprint declares get a control, and everything else in
 * the file is kept exactly as written.
 */
export function GameSettings({ projectId }: { projectId: number }) {
  const project = useProject()
  const overview = useGameOverview(projectId)
  // The strip at the head is the deployment's: a game server has saved
  // changes that are not live like any other project.
  const state = useConfiguration(projectId, project.environmentId)
  return (
    <div className="space-y-6">
      {overview.data ? (
        <GameIdentity overview={overview.data} projectId={projectId} onChanged={overview.refresh} />
      ) : (
        !overview.error && <GameIdentitySkeleton />
      )}
      <SettingsPage state={state}>
        {() => <PropertiesForm projectId={projectId} files={overview.data?.files} />}
      </SettingsPage>
    </div>
  )
}

function PropertiesForm({
  projectId,
  files,
}: {
  projectId: number
  /** The blueprint's files, for the name it gives the one this page edits. */
  files?: BlueprintConfigFile[]
}) {
  const { can } = useAuth()
  const project = useProject()
  const admin = can("system.admin")
  const [draft, setDraft] = useSessionState<Record<string, string>>(
    `deploy.${projectId}.game.settings`,
    {},
  )
  const [saving, setSaving] = useState(false)
  const properties = usePoll(
    (signal) => get<GameProperties>(`/deploy/${projectId}/game/properties`, undefined, signal),
    0,
    [projectId],
  )
  const data = properties.data
  // What the file holds for a key, or what the server assumes when the file
  // does not name it — the one rule the fields, their "was" marks and the
  // count of unsaved changes all read, so a switch turned on and off again is
  // not a change.
  const savedValue = useCallback(
    (key: string) =>
      data?.values?.[key] ?? data?.known.find((property) => property.key === key)?.default ?? "",
    [data],
  )
  const changed = useMemo(
    () => Object.entries(draft).filter(([key, value]) => savedValue(key) !== value),
    [draft, savedValue],
  )
  const errors = useMemo(() => {
    const found: Record<string, string> = {}
    for (const property of data?.known ?? []) {
      const value = draft[property.key]
      if (property.kind !== "number" || value === undefined) continue
      const n = Number(value)
      const { minimum: min, maximum: max } = property
      if (value.trim() === "" || !Number.isFinite(n)) {
        found[property.key] = `${property.label} takes a number`
      } else if ((min !== undefined && n < min) || (max !== undefined && n > max)) {
        found[property.key] =
          min !== undefined && max !== undefined
            ? `${property.label} takes ${min} to ${max}`
            : min !== undefined
              ? `${property.label} takes ${min} or more`
              : `${property.label} takes ${max} or less`
      }
    }
    return found
  }, [data, draft])
  const invalid = changed.some(([key]) => errors[key])
  const groups = useMemo(() => groupProperties(data?.known ?? []), [data])

  const save = async () => {
    if (changed.length === 0 || invalid) return false
    setSaving(true)
    try {
      const result = await put<{ applied: string[]; restartRequired: boolean }>(
        `/deploy/${projectId}/game/properties`,
        { changes: Object.fromEntries(changed) },
      )
      // Saving writes the file and does not apply it, so the toast that says
      // it was written is also where the restart is one press away.
      const restart = result.restartRequired && can("service.control") && project.normalized
      notify.success(
        result.applied.length === 1
          ? `${result.applied[0]} saved`
          : `${result.applied.length} settings saved`,
        result.restartRequired
          ? {
              description: "Restart the server for them to take effect.",
              action: restart
                ? { label: "Restart now", onClick: () => void project.start("restart") }
                : undefined,
            }
          : undefined,
      )
      setDraft({})
      properties.refresh()
      return true
    } catch (error) {
      notify.error("Could not save these settings", error)
      return false
    } finally {
      setSaving(false)
    }
  }

  if (properties.error && !data)
    return <ErrorState error={properties.error} onRetry={properties.refresh} />
  if (!data) return <PropertiesSkeleton />
  if (data.status !== "available")
    return (
      <ProjectEmpty title="The settings file cannot be read right now" reason={data.reason}>
        <EmptyStep href={`/deploy/${projectId}/runtime`}>Runtime</EmptyStep>
        <EmptyStep href={`/deploy/${projectId}/logs`}>Logs</EmptyStep>
      </ProjectEmpty>
    )
  if (data.known.length === 0)
    return (
      <ProjectEmpty title="This template declares no settings to edit here">
        <EmptyStep href={`/deploy/${projectId}/console`}>Console</EmptyStep>
      </ProjectEmpty>
    )

  const current = (property: BlueprintProperty) => draft[property.key] ?? savedValue(property.key)
  return (
    <>
      <SettingForm
        name="Server settings"
        onSave={save}
        dirty={changed.length > 0}
        changes={changed.length}
        saving={saving}
        invalid={invalid}
        canEdit={admin}
        onDiscard={() => setDraft({})}
        applies={data.restartRequired ? "restart" : "immediately"}
        note={!admin && "Changing these needs the system administrator capability."}
      >
        {groups.map((group) => {
          const facts = groupFacts(group.properties, current).join(" · ")
          return (
            <SettingSection
              key={group.id}
              title={group.title}
              state={
                facts && (
                  <span key={facts} className="numeric block animate-rise">
                    {facts}
                  </span>
                )
              }
              status={settingStatus({
                dirty: group.properties.some((property) =>
                  changed.some(([key]) => key === property.key),
                ),
              })}
            >
              <PropertyFields
                known={group.properties}
                saved={savedValue}
                draft={draft}
                errors={errors}
                disabled={!admin}
                onChange={(key, value) => setDraft((previous) => ({ ...previous, [key]: value }))}
              />
            </SettingSection>
          )
        })}
      </SettingForm>
      {data.raw && (
        <RawFile
          raw={data.raw}
          path={data.path}
          label={files?.find((file) => file.path === data.path)?.label}
        />
      )}
    </>
  )
}

/**
 * The form before the file is read: two sections, each a head over its
 * fields, in the column the form takes — so the sections land where their
 * outlines were.
 */
function PropertiesSkeleton() {
  return (
    <>
      {[0, 1].map((section) => (
        <div
          key={section}
          aria-hidden
          className="min-w-0 space-y-4 py-8 first:pt-0 last:pb-0"
          data-slot="setting-skeleton"
        >
          <div className="space-y-2">
            <Skeleton className="h-4 w-20" />
            <Skeleton className="h-3 w-48" />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-9 w-full" />
          </div>
          <Skeleton className="h-9 w-full" />
        </div>
      ))}
    </>
  )
}

/**
 * A section's declared keys, each in the control its kind wants: text full
 * width, numbers and choices two to a row, switches as rows on hairlines.
 *
 * A field whose value differs from the file says what it was at the far end
 * of its own label line, where the key's chip used to be: under the field it
 * pushed every field below it down a line on the first keystroke.
 */
function PropertyFields({
  known,
  saved: savedValue,
  draft,
  errors,
  disabled,
  onChange,
}: {
  known: BlueprintProperty[]
  /** What the file holds for a key, or the server's default when it holds nothing. */
  saved: (key: string) => string
  draft: Record<string, string>
  errors: Record<string, string>
  disabled: boolean
  onChange: (key: string, value: string) => void
}) {
  const saved = (property: BlueprintProperty) => savedValue(property.key)
  const current = (property: BlueprintProperty) => draft[property.key] ?? saved(property)
  // A choice is named as its control shows it, not as the file spells it.
  const shown = (property: BlueprintProperty) =>
    property.choices?.find((choice) => choice.value === saved(property))?.label ?? saved(property)
  const was = (property: BlueprintProperty, value: string) =>
    current(property) !== saved(property) && (
      <span className="max-w-48 animate-rise truncate text-hint text-warning">was {value}</span>
    )
  // The file's own word for the setting, for a reader who knows the file.
  const named = (property: BlueprintProperty) => (
    <HoverTip tip={<span className="font-mono">{property.key}</span>}>
      <span>{property.label}</span>
    </HoverTip>
  )

  const text = known.filter((property) => property.kind === "text")
  const paired = known.filter(
    (property) => property.kind === "number" || property.kind === "choice",
  )
  const switches = known.filter((property) => property.kind === "boolean")

  const field = (property: BlueprintProperty) => {
    const id = `game-property-${property.key}`
    const value = current(property)
    const error = errors[property.key]
    const choices = property.choices ?? []
    const segments = property.kind === "choice" && choices.length <= 4
    return (
      <Field
        key={property.key}
        label={named(property)}
        htmlFor={segments ? undefined : id}
        info={property.description}
        error={error}
        trailing={was(property, `“${shown(property) || "empty"}”`)}
      >
        {property.kind === "choice" ? (
          segments ? (
            <Segments
              label={property.label}
              value={value}
              options={choices}
              disabled={disabled}
              fill
              onChange={(next) => onChange(property.key, next)}
            />
          ) : (
            <Select
              value={value}
              disabled={disabled}
              onValueChange={(next) => onChange(property.key, next)}
            >
              <SelectTrigger id={id} className="w-full">
                <SelectValue placeholder="Choose" />
              </SelectTrigger>
              <SelectContent>
                {choices.map((choice) => (
                  <SelectItem key={choice.value} value={choice.value}>
                    {choice.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )
        ) : property.kind === "number" &&
          property.minimum !== undefined &&
          property.maximum !== undefined ? (
          <InputGroup>
            <InputGroupInput
              id={id}
              type="number"
              inputMode="numeric"
              min={property.minimum}
              max={property.maximum}
              value={value}
              disabled={disabled}
              aria-invalid={Boolean(error)}
              className="numeric"
              onChange={(event) => onChange(property.key, event.target.value)}
            />
            <InputGroupAddon align="inline-end">
              <InputGroupText className="numeric">
                {property.minimum}–{property.maximum}
              </InputGroupText>
            </InputGroupAddon>
          </InputGroup>
        ) : (
          <Input
            id={id}
            value={value}
            disabled={disabled}
            type={property.kind === "number" ? "number" : "text"}
            inputMode={property.kind === "number" ? "numeric" : undefined}
            min={property.minimum}
            max={property.maximum}
            aria-invalid={Boolean(error)}
            onChange={(event) => onChange(property.key, event.target.value)}
          />
        )}
      </Field>
    )
  }

  return (
    <>
      {text.map(field)}
      {paired.length > 0 && <FieldRow columns={2}>{paired.map(field)}</FieldRow>}
      {switches.length > 0 && (
        <OptionList>
          {switches.map((property) => (
            <OptionRow
              key={property.key}
              title={
                // What it was rides the title's own line, so a switch that is
                // turned keeps the height it had.
                <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                  {named(property)}
                  {was(property, saved(property) === "true" ? "on" : "off")}
                </span>
              }
              hint={property.description}
              checked={current(property) === "true"}
              disabled={disabled}
              onCheckedChange={(checked) => onChange(property.key, checked ? "true" : "false")}
            />
          ))}
        </OptionList>
      )}
    </>
  )
}

/**
 * The declared settings as the file writes them — escapes and all — numbered
 * and folded away until wanted. It is not the whole file: the server sends
 * only the keys this page edits, because the rest can hold an RCON password
 * or a plugin's credentials, and the head says so rather than presenting
 * these lines as everything on disk.
 */
function RawFile({ raw, path, label }: { raw: string; path?: string; label?: string }) {
  const lines = raw.replace(/\r\n?/g, "\n").replace(/\n$/, "").split("\n")
  return (
    <SettingSection
      title={label ?? "The file on disk"}
      state={
        <>
          {path && <span className="font-mono text-foreground/80">{path}</span>}
          {path && " · "}
          {plural(lines.length, "line")} edited here · the rest kept as written and not shown
        </>
      }
    >
      <Disclosure summary="View the file on disk" quiet>
        <Well className="max-h-96 px-0 py-2">
          <ol className="min-w-max">
            {lines.map((line, index) => (
              <li key={index} className="flex gap-3 px-3 hover:bg-row-hover">
                <span
                  aria-hidden
                  className="numeric w-7 shrink-0 text-right text-muted-foreground/60 select-none"
                >
                  {index + 1}
                </span>
                <span className="whitespace-pre text-foreground">
                  {line ? <LogText text={line} /> : " "}
                </span>
              </li>
            ))}
          </ol>
        </Well>
      </Disclosure>
    </SettingSection>
  )
}

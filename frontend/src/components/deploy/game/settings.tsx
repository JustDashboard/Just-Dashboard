"use client"

import { useCallback, useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { Slash } from "@/components/icons"
import { get, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { BlueprintProperty, GameProperties } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import {
  Disclosure,
  Field,
  FieldRow,
  FormNote,
  FormSection,
  FormSections,
  OptionList,
  OptionRow,
} from "@/components/form"
import { LogText } from "@/components/logs/log-text"
import { Well } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { SettingFoot } from "@/components/deploy/settings/setting-card"
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useProject } from "@/components/deploy/project-context"
import { GameIdentity, useGameOverview } from "@/components/deploy/game/identity"

/**
 * The server's own settings file, as a page that is a form (§7): the file's
 * name and what this page does to it in a rail beside the fields, and the
 * file itself, as it sits on disk, in a second section folded away.
 *
 * Only the keys the blueprint declares get a control — a line of text full
 * width, the numbers and choices two to a row with a declared range said
 * inside the field, the switches as options with their sentence — and
 * everything else in the file is kept exactly as written.
 *
 * Changing anything raises the settings pages' own foot, which stays at the
 * bottom of the window until it is saved or discarded, so Save never scrolls
 * away with the first two fields on a phone; that the server only reads the
 * file on start is said there, where it matters, rather than in a banner
 * nobody has to act on yet.
 */
export function GameSettings({ projectId }: { projectId: number }) {
  const { can } = useAuth()
  const project = useProject()
  const admin = can("system.admin")
  const [draft, setDraft] = useSessionState<Record<string, string>>(
    `deploy.${projectId}.game.settings`,
    {},
  )
  const [saving, setSaving] = useState(false)
  const overview = useGameOverview(projectId)
  const properties = usePoll(
    (signal) => get<GameProperties>(`/deploy/${projectId}/game/properties`, undefined, signal),
    0,
    [projectId],
  )
  const data = properties.data
  // What the file holds for a key, or what the server assumes when the file
  // does not name it — the one rule the fields, their "was" hints and the
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

  const save = async () => {
    if (changed.length === 0 || invalid) return
    setSaving(true)
    try {
      const result = await put<{ applied: string[]; restartRequired: boolean }>(
        `/deploy/${projectId}/game/properties`,
        { changes: Object.fromEntries(changed) },
      )
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
    } catch (error) {
      notify.error("Could not save these settings", error)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-6">
      {overview.data && <GameIdentity overview={overview.data} />}
      {properties.error ? (
        <ErrorState error={properties.error} />
      ) : !data ? (
        <LoadingRows rows={5} />
      ) : data.status !== "available" ? (
        <Notice title="Settings unavailable" icon={Slash}>
          {data.reason ?? "This file could not be read from the running server."}
        </Notice>
      ) : data.known.length === 0 ? (
        <EmptyNote className="px-0 text-left">
          This blueprint declares no settings this dashboard edits.
        </EmptyNote>
      ) : (
        <form
          aria-label="Server settings"
          onSubmit={(event) => {
            event.preventDefault()
            void save()
          }}
        >
          <FormSections railFrom="xl" className="animate-rise">
            <FormSection
              aside
              title={
                overview.data?.files.find((file) => file.path === data.path)?.label ??
                "Server settings"
              }
              hint={
                <div className="space-y-2">
                  <p>
                    {data.path && <span className="font-mono text-foreground/80">{data.path}</span>}
                    {data.restartRequired && " · read when the server starts"}
                  </p>
                  <p>
                    {plural(data.known.length, "setting")} edited here · every other line kept as
                    written
                  </p>
                  {!admin && (
                    <FormNote className="text-foreground/80">
                      Changing these needs the system administrator capability.
                    </FormNote>
                  )}
                </div>
              }
            >
              <PropertyFields
                known={data.known}
                saved={savedValue}
                draft={draft}
                errors={errors}
                disabled={!admin}
                onChange={(key, value) => setDraft((previous) => ({ ...previous, [key]: value }))}
              />
            </FormSection>
            {data.raw && <RawFile raw={data.raw} />}
          </FormSections>

          <SettingFoot
            dirty={changed.length > 0}
            changes={changed.length}
            saving={saving}
            invalid={invalid}
            canEdit={admin}
            onDiscard={() => setDraft({})}
            note={
              <>
                {changed.length > 0 && (
                  <span className="font-mono">{changed.map(([key]) => key).join(", ")}</span>
                )}
                {data.restartRequired && (
                  <span className={cn("block", changed.length > 0 && "text-warning")}>
                    The server reads this file on start. Saving writes the change; restarting
                    applies it.
                  </span>
                )}
              </>
            }
          />
        </form>
      )}
    </div>
  )
}

/**
 * The declared keys, each in the control its kind wants: text full width,
 * numbers and choices two to a row, switches as options with their sentence.
 * A field whose value differs from the file says what it was, so a reader
 * sees what saving would change without comparing in their head.
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
  const hint = (property: BlueprintProperty) =>
    current(property) !== saved(property)
      ? `was “${shown(property) || "empty"}”`
      : property.description

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
    return (
      <Field
        key={property.key}
        label={property.label}
        htmlFor={property.kind === "choice" && choices.length <= 4 ? undefined : id}
        hint={hint(property)}
        error={error}
        trailing={<Tag mono>{property.key}</Tag>}
      >
        {property.kind === "choice" ? (
          choices.length <= 4 ? (
            <ToggleGroup
              type="single"
              variant="outline"
              size="sm"
              value={value}
              disabled={disabled}
              aria-label={property.label}
              // Pressing the chosen one again is not a way to choose nothing.
              onValueChange={(next) => next && onChange(property.key, next)}
              className="w-full"
            >
              {choices.map((choice) => (
                <ToggleGroupItem
                  key={choice.value}
                  value={choice.value}
                  className="h-11 flex-1 px-2 text-body sm:h-9"
                >
                  {choice.label}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
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
              title={property.label}
              hint={
                current(property) !== saved(property)
                  ? `was ${saved(property) === "true" ? "on" : "off"}`
                  : (property.description ?? <span className="font-mono">{property.key}</span>)
              }
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
 * or a plugin's credentials, and it says so rather than presenting these
 * lines as everything on disk.
 */
function RawFile({ raw }: { raw: string }) {
  const lines = raw.replace(/\r\n?/g, "\n").replace(/\n$/, "").split("\n")
  return (
    <FormSection
      aside
      title="The file on disk"
      hint={
        <p>
          These lines as the file writes them. The rest of the file stays on the server: it can hold
          passwords this page has no reason to show.
        </p>
      }
    >
      <Disclosure summary="View the file on disk">
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
    </FormSection>
  )
}

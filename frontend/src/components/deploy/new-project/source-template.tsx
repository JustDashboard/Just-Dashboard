"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { ArrowUpRight, Warning } from "@/components/icons"
import { get, post } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import type {
  BlueprintDetail,
  BlueprintInput,
  BlueprintSummary,
  DeploymentDraftSource,
  GameImportPreview,
  GameVersionList,
  WorkloadProfile,
} from "@/lib/types"
import { Field } from "@/components/form"
import { Group, Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ROW_BLEED, RowList } from "@/components/row-list"
import { SearchInput } from "@/components/page"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { deploymentName, humanize } from "@/components/deploy/vocabulary"
import { inspectAndPrepare, type ConfigureFlow } from "@/components/deploy/new-project/draft"

const CATEGORY_LABEL: Record<BlueprintSummary["category"], string> = {
  http: "Web applications",
  database: "Databases",
  tool: "Tools",
  automation: "Automation",
  game: "Game servers",
}

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/**
 * The reviewed catalogue, and the chosen blueprint's own inputs — ported from
 * `blueprint-picker.tsx`, minus the profile pre-filter the old intent step
 * gave it: everything the server reviews is shown, grouped the way it groups
 * itself, and one "Use" inspects with whatever inputs are filled in.
 */
export function SourceTemplate({ onInspected }: { onInspected: (flow: ConfigureFlow) => void }) {
  const catalogue = usePoll(
    (signal) => get<BlueprintSummary[]>("/deploy/blueprints/", undefined, signal),
    0,
  )
  const [filter, setFilter] = useSessionState("deploy.new.template.filter", "")
  const [selectedId, setSelectedId] = useSessionState("deploy.new.template.selected", "")
  const [inputs, setInputs] = useSessionState<Record<string, string>>(
    "deploy.new.template.inputs",
    {},
  )
  const [showAdvanced, setShowAdvanced] = useSessionState("deploy.new.template.advanced", false)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<Error>()

  const detail = usePoll(
    (signal) => get<BlueprintDetail>(`/deploy/blueprints/${selectedId}`, undefined, signal),
    0,
    [selectedId],
    { enabled: Boolean(selectedId) },
  )
  const definition = detail.data?.id === selectedId ? detail.data : undefined

  const grouped = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const map = new Map<BlueprintSummary["category"], BlueprintSummary[]>()
    for (const entry of catalogue.data ?? []) {
      if (
        needle &&
        !entry.name.toLowerCase().includes(needle) &&
        !entry.description.toLowerCase().includes(needle)
      )
        continue
      map.set(entry.category, [...(map.get(entry.category) ?? []), entry])
    }
    return [...map.entries()]
  }, [catalogue.data, filter])

  const select = (entry: BlueprintSummary) => {
    setSelectedId(entry.id)
    setInputs({})
    setShowAdvanced(false)
  }

  const declared = definition?.inputs ?? []
  const basic = declared.filter((input) => !input.advanced)
  const advanced = declared.filter((input) => input.advanced)
  const blocked = definition?.deploymentSupported === false

  const use = async () => {
    if (!definition || blocked || busy) return
    setBusy(true)
    setFailure(undefined)
    try {
      const profile: WorkloadProfile = definition.profile === "game" ? "game" : "service"
      const source: DeploymentDraftSource = {
        kind: "blueprint",
        mode: "blueprint",
        blueprintId: definition.id,
        blueprintVersion: definition.version,
        blueprintInputs: inputs,
      }
      onInspected(
        await inspectAndPrepare(deploymentName(definition.name), profile, source, {
          sourceLabel: definition.name,
        }),
      )
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBusy(false)
    }
  }

  const listed = (catalogue.data ?? []).length

  return (
    // The catalogue and the chosen template's own inputs, side by side. They
    // used to be stacked, so choosing one scrolled the catalogue off the
    // screen and comparing two meant scrolling back up to find the first.
    //
    // The second column only exists once there is something to put in it: a
    // reserved empty column reads as a layout bug rather than as a promise,
    // which is the same reason a row's actions are revealed and not merely
    // made transparent.
    <div
      className={cn(
        "grid min-w-0 items-start gap-x-10 gap-y-6",
        definition ? "xl:grid-cols-[minmax(0,1fr)_26rem]" : "max-w-4xl",
      )}
    >
      {failure && <ErrorState error={failure} className="xl:col-span-2" />}
      <Panel plain className="min-w-0">
        <PanelHeader
          title="Application templates"
          actions={
            catalogue.data && (
              <span className="numeric text-hint text-muted-foreground">
                {plural(listed, "template")}
              </span>
            )
          }
        />
        <PanelBody className="space-y-4">
          {catalogue.error && <ErrorState error={catalogue.error} />}
          <SearchInput
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder="Search templates"
            aria-label="Search templates"
            containerClassName="sm:w-full"
          />
          {catalogue.loading && !catalogue.data && <LoadingRows rows={4} />}
          {catalogue.data && grouped.length === 0 && (
            <EmptyNote>No template matches that search.</EmptyNote>
          )}
          {grouped.map(([category, entries]) => (
            <div key={category} className="animate-rise space-y-1">
              <p className="eyebrow">{CATEGORY_LABEL[category] ?? humanize(category)}</p>
              <RowList aria-label={CATEGORY_LABEL[category] ?? humanize(category)}>
                {entries.map((entry) => {
                  const usable = entry.deploymentSupported !== false
                  return (
                    // §12's shape again: the name carries the verb and is the
                    // control; the row around it answers the pointer. A
                    // template that cannot be deployed has no control at all,
                    // so there is nothing to press and nothing announcing one.
                    <li key={entry.id} data-slot="row" className="min-w-0">
                      <div
                        onClick={usable ? () => select(entry) : undefined}
                        data-selected={selectedId === entry.id || undefined}
                        className={cn(
                          "flex min-w-0 items-center gap-3 px-5 py-3 text-left",
                          ROW_BLEED,
                          usable && "cursor-pointer transition-colors hover:bg-row-hover",
                          selectedId === entry.id && "bg-accent hover:bg-accent",
                        )}
                      >
                        <span className="min-w-0 flex-1">
                          {usable ? (
                            <button
                              type="button"
                              aria-label={`Use ${entry.name}`}
                              aria-pressed={selectedId === entry.id}
                              onClick={(event) => {
                                event.stopPropagation()
                                select(entry)
                              }}
                              className="block max-w-full min-w-0 truncate rounded-sm text-body font-medium focus-ring"
                            >
                              {entry.name}
                            </button>
                          ) : (
                            <span className="block truncate text-body font-medium">
                              {entry.name}
                            </span>
                          )}
                          <span className="block truncate text-hint text-muted-foreground">
                            {usable
                              ? entry.description
                              : entry.unavailableReason ||
                                "This blueprint cannot be deployed by this dashboard version."}
                          </span>
                        </span>
                        <span className="flex shrink-0 items-center gap-2">
                          {entry.requiresAcceptance && <Tag tone="warning">licence</Tag>}
                          {entry.privileged && <Tag tone="danger">privileged</Tag>}
                          <Tag mono>{entry.image}</Tag>
                          {!usable && <Tag tone="warning">Preview only</Tag>}
                        </span>
                      </div>
                    </li>
                  )
                })}
              </RowList>
            </div>
          ))}
        </PanelBody>
      </Panel>

      {selectedId && definition && (
        <Panel plain className="min-w-0 xl:sticky xl:top-6">
          <PanelHeader title={definition.name} actions={<Tag mono>v{definition.version}</Tag>} />
          <PanelBody className="space-y-4">
            <Group className="space-y-1.5 text-xs text-muted-foreground">
              <p className="flex flex-wrap items-center gap-2">
                <Tag>{definition.provenance.license}</Tag>
              </p>
              <p>
                Reviewed {definition.provenance.reviewedAt} by {definition.provenance.maintainer}.
                Recommended memory {definition.resources.memoryMb} MB.
              </p>
              {definition.update.notes && <p>{definition.update.notes}</p>}
              <p className="flex flex-wrap gap-3">
                <Link
                  href={definition.docsUrl}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="inline-flex min-h-9 items-center gap-1 underline underline-offset-4 focus-ring"
                >
                  Documentation <ArrowUpRight className="size-3" />
                </Link>
                <Link
                  href={definition.provenance.upstreamUrl}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="inline-flex min-h-9 items-center gap-1 underline underline-offset-4 focus-ring"
                >
                  Upstream project <ArrowUpRight className="size-3" />
                </Link>
              </p>
            </Group>

            {blocked && (
              <Notice tone="warning" icon={Warning} title="Blueprint deployment unavailable">
                {definition.unavailableReason || "Choose a different template."}
              </Notice>
            )}

            {definition.profile === "game" && (
              <ExistingServerImport
                onAdopt={(preview) => {
                  const adopted: Record<string, string> = {}
                  if (preview.version) adopted.version = preview.version
                  const properties = preview.properties ?? {}
                  for (const [key, name] of [
                    ["motd", "server-name"],
                    ["max-players", "max-players"],
                    ["difficulty", "difficulty"],
                  ] as const) {
                    if (properties[key]) adopted[name] = properties[key]
                  }
                  setInputs((current) => ({ ...current, ...adopted }))
                }}
              />
            )}

            {definition.profile === "game" && (
              <GameVersionField
                blueprintId={definition.id}
                value={inputs.version ?? ""}
                onChange={(value) => setInputs((current) => ({ ...current, version: value }))}
              />
            )}

            {basic.map((input) => (
              <BlueprintField
                key={input.name}
                input={input}
                value={inputs[input.name] ?? input.default ?? ""}
                onChange={(value) => setInputs((current) => ({ ...current, [input.name]: value }))}
              />
            ))}

            {advanced.length > 0 && (
              <button
                type="button"
                aria-expanded={showAdvanced}
                onClick={() => setShowAdvanced(!showAdvanced)}
                className="inline-flex min-h-9 items-center text-xs underline underline-offset-4 focus-ring"
              >
                {showAdvanced ? "Hide" : "Show"} advanced blueprint settings
              </button>
            )}
            {showAdvanced &&
              advanced.map((input) => (
                <BlueprintField
                  key={input.name}
                  input={input}
                  value={inputs[input.name] ?? input.default ?? ""}
                  onChange={(value) =>
                    setInputs((current) => ({ ...current, [input.name]: value }))
                  }
                />
              ))}

            {(definition.secrets?.length ?? 0) > 0 && (
              <Notice title="Secrets are generated on this server" icon={Warning}>
                <ul className="list-disc space-y-1 pl-4">
                  {definition.secrets?.map((secret) => (
                    <li key={secret.name}>
                      <b>{secret.label}</b> — {secret.description ?? `${secret.length} characters`}.
                      It is created when this plan is saved and never shipped with the blueprint.
                    </li>
                  ))}
                </ul>
              </Notice>
            )}
          </PanelBody>
          <PanelFooter className="justify-end">
            <Button
              className="h-11 sm:h-9"
              pending={busy}
              disabled={blocked}
              onClick={() => void use()}
            >
              Use this template
            </Button>
          </PanelFooter>
        </Panel>
      )}
    </div>
  )
}

function BlueprintField({
  input,
  value,
  onChange,
}: {
  input: BlueprintInput
  value: string
  onChange: (value: string) => void
}) {
  const id = `blueprint-${input.name}`
  if (input.kind === "accept") {
    return (
      <Label htmlFor={id} className="flex min-h-11 items-start gap-2 text-xs text-foreground">
        <Checkbox
          id={id}
          checked={value === "true"}
          onCheckedChange={(checked) => onChange(checked === true ? "true" : "false")}
        />
        <span className="space-y-1">
          <span className="block font-medium">{input.label}</span>
          {input.description && (
            <span className="block text-muted-foreground">{input.description}</span>
          )}
          {input.acceptUrl && (
            <Link
              href={input.acceptUrl}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1 underline underline-offset-4"
            >
              Read the agreement <ArrowUpRight className="size-3" />
            </Link>
          )}
        </span>
      </Label>
    )
  }
  return (
    <Field label={input.label} htmlFor={id} hint={input.description}>
      {input.kind === "boolean" ? (
        <div className="flex min-h-9 items-center gap-2">
          <Switch
            id={id}
            checked={value === "true"}
            onCheckedChange={(checked) => onChange(checked ? "true" : "false")}
          />
          <span className="text-xs text-muted-foreground">{value === "true" ? "On" : "Off"}</span>
        </div>
      ) : input.kind === "choice" ? (
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger id={id}>
            <SelectValue placeholder="Choose" />
          </SelectTrigger>
          <SelectContent>
            {(input.choices ?? []).map((choice) => (
              <SelectItem key={choice.value} value={choice.value}>
                {choice.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Input
          id={id}
          value={value}
          type={input.kind === "number" || input.kind === "memory" ? "number" : "text"}
          inputMode={input.kind === "number" || input.kind === "memory" ? "numeric" : undefined}
          min={input.minimum}
          max={input.maximum}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
    </Field>
  )
}

/**
 * Minecraft versions come from the upstream manifest. When that cannot be
 * reached, the field says so and still accepts an exact version the operator
 * knows exists — it never substitutes an unverified one.
 */
function GameVersionField({
  blueprintId,
  value,
  onChange,
}: {
  blueprintId: string
  value: string
  onChange: (value: string) => void
}) {
  const versions = usePoll(
    (signal) =>
      get<GameVersionList>(`/deploy/blueprints/${blueprintId}/versions`, undefined, signal),
    0,
    [blueprintId],
  )
  const list = versions.data
  const usable = list?.status === "available" || list?.status === "stale"
  const offered = useMemo(() => {
    const entries = list?.versions ?? []
    if (!value || entries.some((version) => version.id === value)) return entries
    // An imported server may be on a version the upstream list no longer
    // offers; keeping it selectable is the difference between importing that
    // server and silently upgrading it.
    return [{ id: value, kind: "release", recommended: false }, ...entries]
  }, [list?.versions, value])
  return (
    <Field
      label="Minecraft version"
      htmlFor="blueprint-version"
      hint={
        list?.status === "available"
          ? `${list.versions.length} releases read from the upstream manifest.`
          : (list?.reason ?? "Reading the upstream version list…")
      }
    >
      {usable ? (
        <Select value={value || list.recommended || ""} onValueChange={onChange}>
          <SelectTrigger id="blueprint-version">
            <SelectValue placeholder="Choose a version" />
          </SelectTrigger>
          <SelectContent>
            {offered.map((version) => (
              <SelectItem key={version.id} value={version.id}>
                {version.id}
                {version.recommended ? " · recommended" : ""}
                {!list.versions.some((known) => known.id === version.id)
                  ? " · not in the upstream list"
                  : ""}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Input
          id="blueprint-version"
          value={value}
          placeholder="LATEST"
          className="font-mono"
          onChange={(event) => onChange(event.target.value)}
        />
      )}
    </Field>
  )
}

/**
 * An existing server, inspected in place. Nothing is copied, started or
 * changed to produce this: it reads names and the server's own settings
 * file, and says what it concluded and why so the operator can correct it.
 */
function ExistingServerImport({ onAdopt }: { onAdopt: (preview: GameImportPreview) => void }) {
  const [open, setOpen] = useSessionState("deploy.new.template.import.open", false)
  const [path, setPath] = useSessionState("deploy.new.template.import.path", "")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [preview, setPreview] = useState<GameImportPreview | null>(null)

  const inspect = async () => {
    setBusy(true)
    setError("")
    try {
      const result = await post<GameImportPreview>("/deploy/game/import/preview", { path })
      setPreview(result)
      onAdopt(result)
    } catch (caught) {
      setPreview(null)
      setError(caught instanceof Error ? caught.message : String(caught))
    } finally {
      setBusy(false)
    }
  }

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="inline-flex min-h-9 items-center text-xs underline underline-offset-4 focus-ring"
      >
        I already have a server on this machine
      </button>
    )
  }
  return (
    <Group className="space-y-3">
      <Field
        label="Server directory"
        htmlFor="game-import-path"
        hint="Read-only. Nothing is copied or started until you review the plan."
      >
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Input
            id="game-import-path"
            value={path}
            placeholder="/srv/minecraft"
            className="min-w-0 flex-1 font-mono"
            onChange={(event) => setPath(event.target.value)}
          />
          <Button
            size="sm"
            variant="outline"
            disabled={busy || !path.trim()}
            onClick={() => void inspect()}
          >
            {busy ? "Inspecting…" : "Inspect"}
          </Button>
        </div>
      </Field>
      {error && (
        <Notice title="That directory was not usable" tone="warning" icon={Warning}>
          {error}
        </Notice>
      )}
      {preview && (
        <div className="min-w-0 space-y-2 text-xs">
          <p className="flex min-w-0 flex-wrap items-center gap-2">
            <Tag>{preview.edition}</Tag>
            <Tag>{preview.software}</Tag>
            {preview.version && <Tag mono>{preview.version}</Tag>}
            <Tag mono>port {preview.port}</Tag>
            <Tag>{bytes(preview.totalBytes)}</Tag>
            {preview.eulaAccepted ? (
              <Tag tone="success">EULA accepted</Tag>
            ) : (
              <Tag tone="warning">EULA not accepted</Tag>
            )}
          </p>
          <dl className="grid min-w-0 gap-1 sm:grid-cols-[8rem_minmax(0,1fr)]">
            {(
              [
                ["Worlds", preview.worldPaths],
                ["Mods and plugins", preview.modPaths],
                ["Configuration", preview.configPaths],
                ["Left behind", preview.ignoredPaths],
              ] as const
            ).map(([label, values]) => (
              <div key={label} className="contents">
                <dt className="text-muted-foreground">{label}</dt>
                <dd className="min-w-0 break-all">
                  {values.length === 0 ? "None found" : values.join(", ")}
                </dd>
              </div>
            ))}
          </dl>
          {preview.evidence.length > 0 && (
            <div className="space-y-1">
              <p className="text-muted-foreground">How it was identified</p>
              <ul className="list-disc space-y-0.5 pl-4 text-muted-foreground">
                {preview.evidence.map((item) => (
                  <li key={item.path + item.reason} className="break-all">
                    <span className="font-mono">{item.path}</span> — {item.reason}
                  </li>
                ))}
              </ul>
            </div>
          )}
          {preview.warnings.length > 0 && (
            <Notice title="Read before importing" tone="warning" icon={Warning}>
              <ul className="list-disc space-y-1 pl-4">
                {preview.warnings.map((warning) => (
                  <li key={warning}>{warning}</li>
                ))}
              </ul>
            </Notice>
          )}
          <p className="text-muted-foreground">
            The fields above were filled in from this server. Review them, then continue: the world
            is copied into a managed volume when the plan runs.
          </p>
        </div>
      )}
    </Group>
  )
}

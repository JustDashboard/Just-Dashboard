"use client"

import { useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { ArrowUpRight, Warning } from "@/components/icons"
import { EmptyNote, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { usePoll } from "@/hooks/use-poll"
import { get, post } from "@/lib/api"
import { bytes } from "@/lib/format"
import { Button } from "@/components/ui/button"
import type {
  BlueprintDetail,
  GameImportPreview,
  BlueprintInput,
  BlueprintSummary,
  GameVersionList,
  WorkloadProfile,
} from "@/lib/types"
import { Group } from "@/components/panel"

const CATEGORY_LABEL: Record<BlueprintSummary["category"], string> = {
  http: "Web applications",
  database: "Databases",
  tool: "Tools",
  automation: "Automation",
  game: "Game servers",
}

/**
 * The reviewed catalogue, and the fields the chosen blueprint declares.
 *
 * Every control here comes from the server's own copy of the blueprint, so a
 * field this dashboard version does not understand cannot appear, and a value
 * it does not accept is refused when the plan is rendered rather than silently
 * dropped.
 */
export function BlueprintPicker({
  profile,
  blueprintId,
  inputs,
  showAdvanced,
  errors,
  onSelect,
  onInput,
  onInputs,
}: {
  profile: WorkloadProfile
  blueprintId: string
  inputs: Record<string, string>
  showAdvanced: boolean
  errors: Record<string, string>
  onSelect: (id: string, version: string) => void
  onInput: (name: string, value: string) => void
  // onInputs writes several fields at once. Calling onInput in a loop would
  // spread the same stale source each time and keep only the last write.
  onInputs: (values: Record<string, string>) => void
}) {
  const catalogue = usePoll(
    (signal) => get<BlueprintSummary[]>("/deploy/blueprints/", undefined, signal),
    0,
    [],
  )
  const detail = usePoll(
    (signal) => get<BlueprintDetail>(`/deploy/blueprints/${blueprintId}`, undefined, signal),
    0,
    [blueprintId],
    { enabled: Boolean(blueprintId) },
  )
  const offered = useMemo(() => {
    const all = catalogue.data ?? []
    // A game profile offers game blueprints; everything else offers the rest.
    return profile === "game"
      ? all.filter((entry) => entry.profile === "game")
      : all.filter((entry) => entry.profile !== "game")
  }, [catalogue.data, profile])

  const grouped = useMemo(() => {
    const map = new Map<BlueprintSummary["category"], BlueprintSummary[]>()
    for (const entry of offered) {
      map.set(entry.category, [...(map.get(entry.category) ?? []), entry])
    }
    return [...map.entries()]
  }, [offered])

  // Keep the selection inside the offered set when the profile changes.
  const selected = offered.find((entry) => entry.id === blueprintId)
  useEffect(() => {
    if (offered.length === 0 || selected) return
    onSelect(offered[0].id, offered[0].version)
  }, [offered, selected, onSelect])

  const definition = detail.data?.id === blueprintId ? detail.data : undefined
  const declared = definition?.inputs ?? []
  const advanced = declared.filter((input) => input.advanced)
  const basic = declared.filter((input) => !input.advanced)

  return (
    <div className="min-w-0 space-y-5">
      <fieldset className="min-w-0 space-y-2">
        <legend className="text-xs font-medium text-muted-foreground">Blueprint</legend>
        {catalogue.loading && !catalogue.data ? (
          <LoadingRows rows={3} />
        ) : offered.length === 0 ? (
          <EmptyNote>No reviewed blueprint matches this kind of workload.</EmptyNote>
        ) : (
          grouped.map(([category, entries]) => (
            <div key={category} className="min-w-0 space-y-2">
              <p className="text-hint tracking-[0.08em] text-muted-foreground uppercase">
                {CATEGORY_LABEL[category]}
              </p>
              <div className="grid min-w-0 gap-2 sm:grid-cols-2">
                {entries.map((entry) => (
                  <label
                    key={entry.id}
                    className={`min-w-0 cursor-pointer space-y-1.5 rounded-lg border p-3 transition-colors focus-within:ring-2 focus-within:ring-ring ${
                      entry.id === blueprintId
                        ? "border-border bg-accent"
                        : "border-hairline hover:bg-row-hover"
                    }`}
                  >
                    <span className="flex min-w-0 items-start gap-2">
                      <input
                        type="radio"
                        name="blueprint"
                        className="mt-1 size-4 shrink-0"
                        checked={entry.id === blueprintId}
                        onChange={() => onSelect(entry.id, entry.version)}
                      />
                      <span className="min-w-0 flex-1 space-y-1">
                        <span className="flex min-w-0 flex-wrap items-center gap-2">
                          <span className="text-sm font-medium">{entry.name}</span>
                          {entry.requiresAcceptance && <Tag tone="warning">licence</Tag>}
                          {entry.privileged && <Tag tone="danger">privileged</Tag>}
                        </span>
                        <span className="block text-xs text-muted-foreground">
                          {entry.description}
                        </span>
                        <span className="block font-mono text-hint break-all text-muted-foreground">
                          {entry.image}
                        </span>
                      </span>
                    </span>
                  </label>
                ))}
              </div>
            </div>
          ))
        )}
      </fieldset>

      {definition && (
        <Group className="space-y-1.5 text-xs text-muted-foreground">
          <p className="flex min-w-0 flex-wrap items-center gap-2">
            <span className="font-medium text-foreground">{definition.name}</span>
            <Tag mono>v{definition.version}</Tag>
            <Tag>{definition.provenance.license}</Tag>
          </p>
          <p>
            Reviewed {definition.provenance.reviewedAt} by {definition.provenance.maintainer}.
            Recommended memory {definition.resources.memoryMb} MB.
          </p>
          {definition.update.notes && <p>{definition.update.notes}</p>}
          <p className="flex min-w-0 flex-wrap gap-3">
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
      )}

      {definition?.profile === "game" && (
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
            onInputs(adopted)
          }}
        />
      )}

      {definition?.profile === "game" && (
        <GameVersionField
          blueprintId={definition.id}
          value={inputs.version ?? ""}
          onChange={(value) => onInput("version", value)}
        />
      )}

      {basic.map((input) => (
        <BlueprintField
          key={input.name}
          input={input}
          value={inputs[input.name] ?? input.default ?? ""}
          error={errors[`blueprint.${input.name}`]}
          onChange={(value) => onInput(input.name, value)}
        />
      ))}

      {showAdvanced &&
        advanced.map((input) => (
          <BlueprintField
            key={input.name}
            input={input}
            value={inputs[input.name] ?? input.default ?? ""}
            error={errors[`blueprint.${input.name}`]}
            onChange={(value) => onInput(input.name, value)}
          />
        ))}

      {definition && (definition.secrets?.length ?? 0) > 0 && (
        <Notice title="Secrets are generated on this server" icon={Warning}>
          <ul className="list-disc space-y-1 pl-4">
            {definition.secrets?.map((secret) => (
              <li key={secret.name}>
                <b>{secret.label}</b> — {secret.description ?? `${secret.length} characters`}. It is
                created when this plan is saved and never shipped with the blueprint.
              </li>
            ))}
          </ul>
        </Notice>
      )}
    </div>
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
  // An imported server may be on a version the upstream list no longer offers.
  // Keeping it selectable is the difference between importing that server and
  // silently upgrading it.
  const offered = useMemo(() => {
    const entries = list?.versions ?? []
    if (!value || entries.some((version) => version.id === value)) return entries
    return [{ id: value, kind: "release", recommended: false }, ...entries]
  }, [list?.versions, value])
  return (
    <div className="min-w-0 space-y-1.5">
      <Label htmlFor="blueprint-version">Minecraft version</Label>
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
      <p className="text-hint text-muted-foreground">
        {list?.status === "available"
          ? `${list.versions.length} releases read from the upstream manifest.`
          : (list?.reason ?? "Reading the upstream version list…")}
      </p>
    </div>
  )
}

function BlueprintField({
  input,
  value,
  error,
  onChange,
}: {
  input: BlueprintInput
  value: string
  error?: string
  onChange: (value: string) => void
}) {
  const id = `blueprint-${input.name}`
  if (input.kind === "accept") {
    return (
      <div className="min-w-0 space-y-1.5">
        <Label
          htmlFor={id}
          className="flex min-h-11 min-w-0 items-start gap-2 text-xs text-foreground"
        >
          <Checkbox
            id={id}
            checked={value === "true"}
            aria-invalid={Boolean(error)}
            onCheckedChange={(checked) => onChange(checked === true ? "true" : "false")}
          />
          <span className="min-w-0 space-y-1">
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
        {error && <p className="text-xs text-destructive">{error}</p>}
      </div>
    )
  }
  return (
    <div className="min-w-0 space-y-1.5">
      <Label htmlFor={id}>{input.label}</Label>
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
          <SelectTrigger id={id} aria-invalid={Boolean(error)}>
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
          aria-invalid={Boolean(error)}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
      {input.description && <p className="text-hint text-muted-foreground">{input.description}</p>}
      {input.kind === "choice" &&
        (input.choices ?? []).find((choice) => choice.value === value)?.description && (
          <p className="text-hint text-muted-foreground">
            {(input.choices ?? []).find((choice) => choice.value === value)?.description}
          </p>
        )}
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  )
}

/**
 * An existing server, inspected in place. Nothing is copied, started or changed
 * to produce this: it reads names and the server's own settings file, and says
 * what it concluded and why so the operator can correct it.
 */
function ExistingServerImport({ onAdopt }: { onAdopt: (preview: GameImportPreview) => void }) {
  const [open, setOpen] = useState(false)
  const [path, setPath] = useState("")
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
      <div className="min-w-0 space-y-1.5">
        <Label htmlFor="game-import-path">Server directory</Label>
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Input
            id="game-import-path"
            value={path}
            placeholder="/srv/minecraft"
            className="min-w-0 flex-1 font-mono"
            onChange={(event) => setPath(event.target.value)}
          />
          <Button size="sm" variant="outline" disabled={busy || !path.trim()} onClick={inspect}>
            {busy ? "Inspecting…" : "Inspect"}
          </Button>
        </div>
        <p className="text-hint text-muted-foreground">
          Read-only. Nothing is copied or started until you review the plan.
        </p>
      </div>
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

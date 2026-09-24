"use client"

import { useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { ArrowUpRight, GridMasonry, Warning } from "@/components/icons"
import { get, post } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { useSessionState } from "@/lib/view-state"
import type {
  BlueprintDetail,
  BlueprintInput,
  BlueprintSummary,
  DeploymentDraftSource,
  DeploymentHostnameSuggestion,
  GameImportPreview,
  GameVersionList,
  WorkloadProfile,
} from "@/lib/types"
import { Disclosure, Field, FormNote, OptionRow } from "@/components/form"
import { FlowActions, FlowPanel, FlowPanelBody, FlowPanelHeader } from "@/components/flow"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Group, Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
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
import { AccessPromise, AccessTag } from "@/components/deploy/first-sign-in"
import { deploymentName } from "@/components/deploy/vocabulary"
import { ProductLogo, ProductLogos, hasProductLogo } from "@/components/product-logo"
import { inspectAndPrepare, type ConfigureFlow } from "@/components/deploy/new-project/draft"
import { useSourceInspection } from "./use-source-inspection"
import { templateInputErrors } from "./template-inputs"

/**
 * What a template is *for*, which is how somebody looking for one thinks.
 *
 * The server's own `category` says how a blueprint is deployed — behind the
 * proxy, as a tool, as a game server — and grouped by it the catalogue opened
 * on twenty-eight "Web applications" in one run: a film server beside a
 * password manager beside a URL shortener. These are the shelves a reader
 * scans instead, in the order they are reached for. A blueprint the server
 * adds without a shelf here lands on the one its category implies, so a new
 * template is never missing from the picker.
 */
const TOPICS = [
  { key: "productivity", label: "Productivity" },
  { key: "media", label: "Media" },
  { key: "monitoring", label: "Monitoring" },
  { key: "automation", label: "Automation" },
  { key: "ai", label: "AI" },
  { key: "developer", label: "Developer tools" },
  { key: "databases", label: "Databases" },
  { key: "web", label: "Web & files" },
  { key: "games", label: "Game servers" },
] as const

type TopicKey = (typeof TOPICS)[number]["key"]

const TOPIC_OF: Record<string, TopicKey> = {
  actual: "productivity",
  docuseal: "productivity",
  drawio: "productivity",
  freshrss: "productivity",
  linkding: "productivity",
  memos: "productivity",
  nextcloud: "productivity",
  nocodb: "productivity",
  "stirling-pdf": "productivity",
  trilium: "productivity",
  vaultwarden: "productivity",
  wallabag: "productivity",
  audiobookshelf: "media",
  jellyfin: "media",
  kavita: "media",
  navidrome: "media",
  seerr: "media",
  beszel: "monitoring",
  dozzle: "monitoring",
  grafana: "monitoring",
  healthchecks: "monitoring",
  metabase: "monitoring",
  prometheus: "monitoring",
  "uptime-kuma": "monitoring",
  gotify: "automation",
  n8n: "automation",
  ntfy: "automation",
  ollama: "ai",
  "open-webui": "ai",
  adminer: "developer",
  "code-server": "developer",
  cyberchef: "developer",
  directus: "developer",
  gitea: "developer",
  "it-tools": "developer",
  jupyter: "developer",
  "mongo-express": "developer",
  opengist: "developer",
  pgadmin: "developer",
  phpmyadmin: "developer",
  portainer: "developer",
  whoami: "developer",
  caddy: "web",
  filebrowser: "web",
  homepage: "web",
  minio: "web",
  "nginx-static": "web",
  searxng: "web",
  shlink: "web",
  syncthing: "web",
}

const CATEGORY_TOPIC: Record<BlueprintSummary["category"], TopicKey> = {
  http: "web",
  database: "databases",
  tool: "developer",
  automation: "automation",
  game: "games",
}

function topicOf(entry: BlueprintSummary): TopicKey {
  return TOPIC_OF[entry.id] ?? CATEGORY_TOPIC[entry.category] ?? "web"
}

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/**
 * The reviewed catalogue, and the chosen blueprint's own inputs — ported from
 * `blueprint-picker.tsx`, minus the profile pre-filter the old intent step
 * gave it: everything the server reviews is shown, on the shelves above, and
 * one "Use" inspects with whatever inputs are filled in.
 */
export function SourceTemplate({
  onInspected,
  initialTemplate,
}: {
  onInspected: (flow: ConfigureFlow) => void
  /** A blueprint id from the address, chosen on arrival. */
  initialTemplate?: string
}) {
  const catalogue = usePoll(
    (signal) => get<BlueprintSummary[]>("/deploy/blueprints/", undefined, signal),
    0,
  )
  const [filter, setFilter] = useSessionState("deploy.new.template.filter", "")
  const [topic, setTopic] = useSessionState<TopicKey | "all">("deploy.new.template.topic", "all")
  const [selectedId, setSelectedId] = useSessionState(
    "deploy.new.template.selected",
    "",
    initialTemplate,
  )
  const [inputs, setInputs] = useSessionState<Record<string, string>>(
    `deploy.new.template.inputs.${selectedId}`,
    {},
  )
  const [showAdvanced, setShowAdvanced] = useSessionState(
    `deploy.new.template.advanced.${selectedId}`,
    false,
  )
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<Error>()
  const inspection = useSourceInspection(onInspected)

  const detail = usePoll(
    (signal) => get<BlueprintDetail>(`/deploy/blueprints/${selectedId}`, undefined, signal),
    0,
    [selectedId],
    { enabled: Boolean(selectedId) },
  )
  const definition = detail.data?.id === selectedId ? detail.data : undefined

  // Every shelf that has a match, in shelf order — the chips count what the
  // search left on each, so a search that empties a shelf says so before the
  // shelf is opened.
  const shelves = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const map = new Map<TopicKey, BlueprintSummary[]>()
    for (const entry of catalogue.data ?? []) {
      if (
        needle &&
        !entry.name.toLowerCase().includes(needle) &&
        !entry.description.toLowerCase().includes(needle)
      )
        continue
      const key = topicOf(entry)
      map.set(key, [...(map.get(key) ?? []), entry])
    }
    return TOPICS.filter((shelf) => map.has(shelf.key)).map((shelf) => ({
      ...shelf,
      entries: map.get(shelf.key)!,
    }))
  }, [catalogue.data, filter])
  const shown = topic === "all" ? shelves : shelves.filter((shelf) => shelf.key === topic)
  const matched = shelves.reduce((total, shelf) => total + shelf.entries.length, 0)

  const revealOnSmallScreen = (id: string) => {
    if (window.matchMedia("(min-width: 1280px)").matches) return
    requestAnimationFrame(() => {
      const panel = document.getElementById(id)
      panel?.focus({ preventScroll: true })
      panel?.scrollIntoView({
        block: "start",
        behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches
          ? "instant"
          : "smooth",
      })
    })
  }
  const select = (entry: BlueprintSummary) => {
    if (entry.id !== selectedId) {
      inspection.cancel()
      setBusy(false)
      setSelectedId(entry.id)
      setAttempted(false)
      setFailure(undefined)
    }
    revealOnSmallScreen("template-settings")
  }

  const declared = definition?.inputs ?? []
  // A field that must be filled is never behind a fold. Everything else about
  // `advanced` holds; what does not hold is hiding the one answer without
  // which the template cannot be used at all.
  const basic = declared.filter((input) => !input.advanced || input.required)
  const advanced = declared.filter((input) => input.advanced && !input.required)
  const blocked = definition?.deploymentSupported === false

  /**
   * The public name this template's own application will be told it answers
   * to — asked for under the name the project is about to be created with,
   * which is the same question the public-address field asks later.
   *
   * Eight of the reviewed definitions declare a required `domain`: n8n writes
   * it into every webhook URL, Vaultwarden into its WebAuthn origin, Nextcloud
   * refuses any other host. That is a real requirement of those applications
   * and not something the dashboard invented — but nobody's first server owns
   * a domain, and the answer already exists: `/deploy/hostname` returns a
   * `<slug>.<address>.sslip.io` name that resolves to this host with no record
   * to create. A `domain` input *is* the plan's domain — `Render` puts it in
   * `plan.Domains` — so filling it in here is the same act as setting the
   * route, and the application's idea of its URL cannot drift from the route
   * that serves it.
   */
  const wantsDomain = declared.some((input) => input.kind === "domain")
  const projectName = definition ? deploymentName(definition.name) : ""
  const suggestion = usePoll(
    (signal) =>
      get<DeploymentHostnameSuggestion>(
        `/deploy/hostname?name=${encodeURIComponent(projectName)}`,
        undefined,
        signal,
      ),
    0,
    [projectName],
    { enabled: wantsDomain && Boolean(projectName) },
  )
  const suggested =
    suggestion.data && suggestion.data.method !== "none" ? suggestion.data.hostname : undefined
  useEffect(() => {
    if (!definition || !suggested) return
    setInputs((current) => {
      // Keyed on presence, not on emptiness: a domain the operator cleared on
      // purpose stays cleared rather than being filled in again under them.
      const seeded = { ...current }
      let changed = false
      for (const input of definition.inputs ?? []) {
        if (input.kind !== "domain" || input.default || input.name in seeded) continue
        seeded[input.name] = suggested
        changed = true
      }
      return changed ? seeded : current
    })
  }, [definition, suggested, setInputs])

  /**
   * What the reviewed definition will refuse, asked here instead.
   *
   * `Render` rejects a missing required input with `"domain" is required` —
   * after the draft has been created, named and saved, and phrased as the
   * server's own field name. The same rule, run before the press, names the
   * field the reader is looking at and costs nothing.
   */
  const [attempted, setAttempted] = useState(false)
  const errors = templateInputErrors(declared, inputs)
  const invalid = declared.filter((input) => errors[input.name])

  const use = async () => {
    if (!definition || blocked || busy) return
    setAttempted(true)
    if (invalid.length > 0) {
      if (invalid.some((input) => input.advanced)) setShowAdvanced(true)
      setFailure(
        new Error(invalid.map((input) => `${input.label}: ${errors[input.name]}`).join(" ")),
      )
      return
    }
    setBusy(true)
    setFailure(undefined)
    let current = true
    try {
      const profile: WorkloadProfile = definition.profile === "game" ? "game" : "service"
      const source: DeploymentDraftSource = {
        kind: "blueprint",
        mode: "blueprint",
        blueprintId: definition.id,
        blueprintVersion: definition.version,
        blueprintInputs: Object.fromEntries(
          declared
            .filter((input) => input.kind !== "secret" && input.name in inputs)
            .map((input) => [input.name, inputs[input.name]]),
        ),
      }
      current = await inspection.inspect(() =>
        inspectAndPrepare(deploymentName(definition.name), profile, source, {
          sourceLabel: definition.name,
        }),
      )
    } catch (error) {
      setFailure(asError(error))
    } finally {
      if (current) setBusy(false)
    }
  }

  const listed = (catalogue.data ?? []).length
  const featured = shown
    .flatMap((shelf) => shelf.entries.map((entry) => entry.id))
    .filter(hasProductLogo)
    .slice(0, 3)
  const chosen = catalogue.data?.find((entry) => entry.id === selectedId)

  return (
    // The catalogue and the chosen template's own inputs, side by side, each
    // the height of the window with its own scroll: choosing a template never
    // scrolls the catalogue away, and comparing two never means scrolling back
    // up to find the first. The second column is always there at this width —
    // it says what will open in it until something does, so choosing the
    // first template does not reflow every card in the grid.
    <div className="grid min-w-0 items-start gap-x-6 gap-y-6 xl:h-full xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_24rem] xl:grid-rows-[minmax(0,1fr)] xl:items-stretch">
      <Panel plain id="template-catalogue" tabIndex={-1} className="min-w-0 scroll-mt-4 xl:min-h-0">
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
        <PanelBody className="flex min-h-0 flex-1 flex-col gap-3">
          {catalogue.error && <ErrorState error={catalogue.error} />}
          <SearchInput
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder="Search templates"
            aria-label="Search templates"
            containerClassName="sm:w-full"
          />
          {catalogue.data && (
            // The shelves, as the Git page's filters are drawn: a chip narrows
            // the catalogue to one shelf and says how many are on it.
            <ChipStrip role="group" aria-label="Template topics">
              <FilterChip selected={topic === "all"} onClick={() => setTopic("all")}>
                All <ChipCount>{matched}</ChipCount>
              </FilterChip>
              {TOPICS.map((shelf) => {
                const count = shelves.find((one) => one.key === shelf.key)?.entries.length ?? 0
                return (
                  <FilterChip
                    key={shelf.key}
                    selected={topic === shelf.key}
                    onClick={() => setTopic(topic === shelf.key ? "all" : shelf.key)}
                  >
                    {shelf.label} <ChipCount>{count}</ChipCount>
                  </FilterChip>
                )
              })}
            </ChipStrip>
          )}
          {catalogue.loading && !catalogue.data && <LoadingRows rows={4} />}
          {catalogue.data && shown.length === 0 && (
            <EmptyNote>
              {matched === 0
                ? "No template matches that search."
                : "Nothing on this shelf matches."}
            </EmptyNote>
          )}
          {/* Padded by the cards' own lit edge, which would otherwise run
              under the scrollbar, and pulled back out so the cards still start
              where the search field does. */}
          <div className="-mx-3 space-y-5 px-3 pt-1 pb-3 xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
            {shown.map((shelf) => (
              // The cards arrive staggered by their own index, so the group
              // does not also rise: that is one block animating twice.
              <div key={shelf.key} className="space-y-2">
                <p className="eyebrow">{shelf.label}</p>
                {/* Kinds, so cards (§16). The shelf is the grid's
                    `role="group"` name — a bare `aria-label` on a div names
                    nothing, so the eyebrow would reach a reader and no one
                    else. */}
                <ChoiceGrid columns="fill" role="group" aria-label={shelf.label}>
                  {shelf.entries.map((entry, index) => {
                    const usable = entry.deploymentSupported !== false
                    return (
                      <ChoiceCard
                        key={entry.id}
                        index={index}
                        logo={<ProductLogo id={entry.id} />}
                        title={entry.name}
                        // The control's name is the verb and the template, not
                        // the card's whole contents — see ChoiceCard (§12).
                        verb={`Use ${entry.name}`}
                        description={
                          usable
                            ? entry.description
                            : entry.unavailableReason ||
                              "This blueprint cannot be deployed by this dashboard version."
                        }
                        // How it is signed into stays on the card, where the
                        // template is chosen (first-sign-in.tsx). The image
                        // moved to the settings beside it: a registry path in
                        // every card was the widest thing in the grid and what
                        // made one card a line taller than its neighbour.
                        trailing={
                          <span className="mt-auto flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 pt-0.5">
                            <AccessTag access={entry.access} />
                            {entry.requiresAcceptance && <Tag tone="warning">licence</Tag>}
                            {entry.privileged && <Tag tone="danger">privileged</Tag>}
                            {!usable && <Tag tone="warning">Preview only</Tag>}
                          </span>
                        }
                        selected={selectedId === entry.id}
                        // A template this dashboard cannot deploy is still
                        // drawn and still says why, and is dimmed and
                        // unpressable rather than silently inert: when the
                        // card is the control, a card with the control taken
                        // out of it looks exactly like one that works.
                        disabled={!usable}
                        onClick={() => select(entry)}
                      />
                    )
                  })}
                </ChoiceGrid>
              </div>
            ))}
          </div>
        </PanelBody>
      </Panel>

      {selectedId ? (
        // The one surface with depth on this screen (§16). Until a template is
        // chosen there is no foreground at all and the advance is the cards'
        // own lit edges; once one is chosen, what is being decided is its
        // inputs and the command that leaves the screen, so that is what takes
        // the depth. The catalogue stays plain: a second framed surface is two
        // foregrounds, which is none.
        <FlowPanel
          id="template-settings"
          aria-label="Selected template settings"
          tabIndex={-1}
          className="order-first min-w-0 scroll-mt-4 xl:order-last xl:max-h-full xl:min-h-0 xl:self-start"
          aria-busy={detail.loading || busy}
        >
          <FlowPanelHeader
            title={
              <span className="flex min-w-0 items-center gap-2.5">
                <ProductLogo id={selectedId} size="sm" />
                <span className="truncate">{definition?.name ?? chosen?.name ?? "Template"}</span>
              </span>
            }
            actions={definition && <Tag mono>v{definition.version}</Tag>}
          />
          <FlowPanelBody className="space-y-4 xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
            {detail.loading && (
              <div role="status" aria-label="Loading template settings">
                <LoadingRows rows={6} />
              </div>
            )}
            {detail.error && <ErrorState error={detail.error} onRetry={detail.refresh} />}
            {/* Beside the command that raised it: what "Use this template"
                refused, or what the inspection it started ran into. */}
            {failure && <ErrorState error={failure} />}
            {definition && (
              <fieldset disabled={busy} className="min-w-0 space-y-4">
                {/* Facts, not a fenced block: inside the one framed surface a
                hairline box is a frame drawn inside a frame. */}
                <div className="min-w-0 space-y-1.5 text-xs text-muted-foreground">
                  <p className="flex min-w-0 flex-wrap items-center gap-2">
                    <Tag>{definition.provenance.license}</Tag>
                    <Tag mono className="max-w-full truncate">
                      {definition.image.reference}
                    </Tag>
                  </p>
                  <p>
                    Reviewed {definition.provenance.reviewedAt} by{" "}
                    {definition.provenance.maintainer}. Recommended memory{" "}
                    {definition.resources.memoryMb} MB.
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
                </div>

                {blocked && (
                  <Notice tone="warning" icon={Warning} title="Blueprint deployment unavailable">
                    {definition.unavailableReason || "Choose a different template."}
                  </Notice>
                )}

                <AccessPromise access={definition.access} />

                {definition.profile === "game" && (
                  <ExistingServerImport
                    key={definition.id}
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
                    error={attempted ? errors[input.name] : undefined}
                    suggested={input.kind === "domain" ? suggestion.data : undefined}
                    onChange={(value) =>
                      setInputs((current) => ({ ...current, [input.name]: value }))
                    }
                  />
                ))}

                {/* A fold inside the settings, drawn as the other folds on these
                    screens are (§7) — it was an underlined 12px text button.
                    Controlled, so a failed validation on a field in here can
                    open it; the fields mount only while it is open. */}
                {advanced.length > 0 && (
                  <Disclosure
                    quiet
                    summary="Advanced blueprint settings"
                    open={showAdvanced}
                    onOpenChange={setShowAdvanced}
                  >
                    {showAdvanced && (
                      <div className="space-y-4">
                        {advanced.map((input) => (
                          <BlueprintField
                            key={input.name}
                            input={input}
                            value={inputs[input.name] ?? input.default ?? ""}
                            error={attempted ? errors[input.name] : undefined}
                            onChange={(value) =>
                              setInputs((current) => ({ ...current, [input.name]: value }))
                            }
                          />
                        ))}
                      </div>
                    )}
                  </Disclosure>
                )}

                {/* What will happen by itself is a line of hint, not a notice
                    (§14): nobody has anything to decide about these. */}
                {(definition.secrets?.length ?? 0) > 0 && (
                  <div className="space-y-1.5">
                    <FormNote>
                      Generated on this server when the plan is saved, and never shipped with the
                      blueprint:
                    </FormNote>
                    <ul className="min-w-0 space-y-1 text-hint leading-relaxed">
                      {definition.secrets?.map((secret) => (
                        <li key={secret.name} className="min-w-0">
                          <span className="text-foreground">{secret.label}</span>
                          <span className="text-muted-foreground">
                            {" — "}
                            {secret.description ?? `${secret.length} characters`}
                          </span>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </fieldset>
            )}
          </FlowPanelBody>
          {/* The one brand-faced command on the screen, at the foot of the
              surface it acts on. */}
          <FlowActions
            secondary={
              <Button
                variant="ghost"
                className="h-11 xl:hidden"
                onClick={() => revealOnSmallScreen("template-catalogue")}
              >
                Choose another template
              </Button>
            }
          >
            <Button
              className="h-11 sm:h-9"
              pending={busy}
              disabled={!definition || blocked}
              onClick={() => void use()}
            >
              Use this template
            </Button>
          </FlowActions>
        </FlowPanel>
      ) : (
        // Only at the width that draws the two columns: stacked, a promise
        // above the catalogue is a block of nothing between the reader and it.
        <EmptyState
          className="hidden xl:flex xl:self-start"
          icon={GridMasonry}
          // What opens here is one of these, so the promise is drawn as three
          // of them rather than as a grid glyph on a plate.
          mark={featured.length > 0 ? <ProductLogos ids={featured} size="md" /> : undefined}
          title="Pick a template"
          description="Its settings open here. Nothing is created until you deploy it."
        />
      )}
    </div>
  )
}

function BlueprintField({
  input,
  value,
  error,
  suggested,
  onChange,
}: {
  input: BlueprintInput
  value: string
  /** The definition requires this and it is empty, and Use has been pressed. */
  error?: string
  /** Where a pre-filled hostname came from, so the field can say so. */
  suggested?: DeploymentHostnameSuggestion
  onChange: (value: string) => void
}) {
  const id = `blueprint-${input.name}`
  if (input.kind === "secret") {
    return (
      <Notice title={input.label}>
        {input.description} Connect a database or enter {input.variable ?? input.name} in the
        Variables step. Credentials stay out of the template settings.
      </Notice>
    )
  }
  if (input.kind === "accept") {
    return (
      <Label
        htmlFor={id}
        className="flex min-h-11 items-start gap-2 text-body leading-snug text-foreground"
      >
        <Checkbox
          id={id}
          checked={value === "true"}
          onCheckedChange={(checked) => onChange(checked === true ? "true" : "false")}
        />
        <span className="space-y-1">
          <span className="block font-medium">{input.label}</span>
          {input.description && (
            <span className="block text-hint font-normal text-muted-foreground">
              {input.description}
            </span>
          )}
          {input.acceptUrl && (
            <Link
              href={input.acceptUrl}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1 text-hint font-normal underline underline-offset-4"
            >
              Read the agreement <ArrowUpRight className="size-3" />
            </Link>
          )}
          {error && (
            <span role="alert" className="block text-hint font-normal text-destructive">
              {error}
            </span>
          )}
        </span>
      </Label>
    )
  }
  // A yes-or-no answer is an option, not a field: its label is the sentence
  // and the switch answers it (§7). It was a switch under a label with "On"
  // or "Off" written beside it — the state said twice, once in a word 12px
  // high.
  if (input.kind === "boolean") {
    // The error stays with it: a required yes-or-no with no default is
    // "Required." until answered, and without the line Use this template was
    // refused with nothing on screen saying why.
    return (
      <div className="space-y-1.5">
        <OptionRow
          title={input.label}
          hint={input.description}
          checked={value === "true"}
          onCheckedChange={(checked) => onChange(checked ? "true" : "false")}
        />
        {error && (
          <p role="alert" className="text-hint leading-relaxed text-destructive">
            {error}
          </p>
        )}
      </div>
    )
  }
  return (
    <Field
      label={input.label}
      htmlFor={id}
      hint={
        suggested && value === suggested.hostname && suggested.method === "sslip"
          ? `${suggested.detail} Replace it with your own domain if you have one.`
          : input.description
      }
      error={error}
      trailing={input.required && <span className="text-hint text-muted-foreground">Required</span>}
    >
      {input.kind === "choice" ? (
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger id={id} className="w-full">
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
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger id="blueprint-version" className="w-full">
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
        hint="Read-only settings inspection. World files, mods and plugins are not copied."
      >
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Input
            id="game-import-path"
            disabled={busy}
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
            The fields above were filled in from this server. This creates a new server; transfer
            your world files, mods and plugins separately if you want to keep them.
          </p>
        </div>
      )}
    </Group>
  )
}

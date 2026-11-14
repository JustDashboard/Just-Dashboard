"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { Lightning, Pause, Pencil, Play, Plus, Trash } from "@/components/icons"
import { ApiError, del, get, post, put } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentSummary, LogDrain, LogDrainKind, LogDrainStream } from "@/lib/types"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { SidePanel } from "@/components/side-panel"
import { Status } from "@/components/status-dot"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FieldRow, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { VerbActions, type Verb } from "@/components/verbs"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"
import { ProjectMark } from "@/components/deploy/project-mark"
import { WireHost, WireMark, WirePlaceholder } from "@/components/deploy/wire"
import {
  noProjectNode,
  projectNode,
  projectNodeId,
  useFleet,
} from "@/components/deploy/section-picture"
import {
  DRAIN_PRODUCT,
  drainFigure,
  drainLine,
  drainStateWord,
  drainTakes,
  drainsCount,
} from "@/components/deploy/log-drain-state"
import {
  drainEndpointProblem,
  drainHealth,
  drainReach,
  traceLinkProblem,
  type DrainHeaderDraft,
} from "@/components/deploy/log-drains"

const KINDS: {
  kind: LogDrainKind
  title: string
  short: string
  hint: string
  placeholder: string
}[] = [
  {
    kind: "otlp",
    title: "OpenTelemetry",
    short: "OTLP over HTTP",
    hint: "OTLP over HTTP: a Collector, Grafana Cloud, Honeycomb, Axiom, Datadog, New Relic and more.",
    placeholder: "http://collector:4318",
  },
  {
    kind: "loki",
    title: "Grafana Loki",
    short: "Loki's push API",
    hint: "Loki's push API, self-hosted or Grafana Cloud Logs.",
    placeholder: "http://loki:3100",
  },
  {
    kind: "http",
    title: "JSON lines over HTTP",
    short: "One JSON object per line",
    hint: "One JSON object per line: Vector, Fluent Bit, Logstash, Axiom's ingest API.",
    placeholder: "https://api.axiom.co/v1/datasets/deploys/ingest",
  },
]

const KIND_TITLE: Record<LogDrainKind, string> = {
  otlp: "OpenTelemetry",
  loki: "Loki",
  http: "JSON lines",
}

/** A format's own mark, bare, beside its name in the sheet's chooser. */
function KindMark({ kind, className }: { kind: LogDrainKind; className?: string }) {
  return <ProductGlyph id={DRAIN_PRODUCT[kind]} className={className} />
}

// The drains and the formats not added yet that the picture has room for;
// past it the rest are one "N more" node and the list names them.
const PICTURE_TARGETS = 5

const ENDPOINT_HINT: Record<LogDrainKind, string> = {
  otlp: "The OTLP base address, as an exporter takes it: /v1/logs, and /v1/traces for traces, are added unless already there.",
  loki: "Loki's address: /loki/api/v1/push is added unless it is already there.",
  http: "Records are POSTed here as application/x-ndjson, gzip-compressed.",
}

const SUGGESTED_HEADERS: Record<LogDrainKind, string[]> = {
  otlp: ["Authorization"],
  loki: ["Authorization", "X-Scope-OrgID"],
  http: ["Authorization"],
}

type Draft = {
  id?: number
  kind: LogDrainKind
  name: string
  endpoint: string
  enabled: boolean
  output: boolean
  requests: boolean
  /** Only an OpenTelemetry drain sends them. */
  traces: boolean
  traceUrl: string
  everyProject: boolean
  projects: number[]
  /** Undefined keeps the saved headers on an edit. */
  headers?: DrainHeaderDraft[]
}

function emptyDraft(kind: LogDrainKind): Draft {
  return {
    kind,
    name: "",
    endpoint: "",
    enabled: true,
    output: true,
    requests: true,
    traces: false,
    traceUrl: "",
    everyProject: true,
    projects: [],
    // No row until one is asked for: a collector on the local network often
    // takes no credential, and an empty row would hold Save back.
    headers: [],
  }
}

function draftOf(drain: LogDrain): Draft {
  return {
    id: drain.id,
    kind: drain.kind,
    name: drain.name,
    endpoint: drain.endpoint,
    enabled: drain.enabled,
    output: drain.streams.includes("output"),
    requests: drain.streams.includes("requests"),
    traces: drain.streams.includes("traces"),
    traceUrl: drain.traceUrl ?? "",
    everyProject: drain.projects.length === 0,
    projects: drain.projects,
  }
}

/**
 * Log drains: where deployments' output and requests are copied while they
 * happen. The page reads as Notifications beside it does: a picture of the
 * projects, this server and each drain as the service it sends to, then a
 * one-line card per drain — where it sends and what, a dot and a word for how
 * it is doing, and the one figure that matters most. The destination's own
 * words, when it refuses, are a press on that word away. The sheet asks for
 * the format, the address and the credential, then what to send.
 */
export function LogDrainsPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const drains = usePoll(
    (signal) => get<LogDrain[]>("/deploy/log-drains", undefined, signal),
    5000,
    [],
  )
  const fleet = useFleet()
  const projects = fleet.projects
  // Beside the name where a row has the room, under it on a phone, as a
  // channel's card does.
  const wide = useMediaQuery("(min-width: 640px)")
  const [draft, setDraft] = useState<Draft>()
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string>()
  const { confirm, dialog } = useConfirm()
  const list = drains.data

  const open = (next: Draft) => {
    setFormError(undefined)
    setDraft(next)
  }
  const close = () => {
    setDraft(undefined)
    setFormError(undefined)
  }

  const save = async () => {
    if (!draft) return
    setSaving(true)
    setFormError(undefined)
    const streams: LogDrainStream[] = []
    if (draft.output) streams.push("output")
    if (draft.requests) streams.push("requests")
    const traces = draft.traces && draft.kind === "otlp"
    if (traces) streams.push("traces")
    const body = {
      name: draft.name.trim(),
      kind: draft.kind,
      endpoint: draft.endpoint.trim(),
      enabled: draft.enabled,
      streams,
      ...(traces ? { traceUrl: draft.traceUrl.trim() } : {}),
      projects: draft.everyProject ? [] : draft.projects,
      // Omitted on an edit that did not touch them, so the sealed ones stay.
      headers: draft.headers?.filter((header) => header.name.trim() || header.value),
    }
    try {
      if (draft.id) await put(`/deploy/log-drains/${draft.id}`, body)
      else await post("/deploy/log-drains", body)
      notify.success(draft.id ? `${body.name} saved` : `${body.name} added`)
      close()
      drains.refresh()
    } catch (err) {
      // A refusal of what was typed is said under the form, where it can be
      // corrected, rather than in a toast that leaves with the answer.
      if (err instanceof ApiError && err.status < 500) setFormError(err.message)
      else notify.error("Could not save the drain", err)
    } finally {
      setSaving(false)
    }
  }

  const test = async (drain: LogDrain) => {
    try {
      await post(`/deploy/log-drains/${drain.id}/test`)
      notify.success(`${drain.name} took the test record`, {
        description: "Look for “Test record from Just Dashboard” at the destination.",
      })
    } catch (err) {
      notify.error(`${drain.name} did not take the test record`, err)
    }
  }

  const setEnabled = async (drain: LogDrain, enabled: boolean) => {
    try {
      await put(`/deploy/log-drains/${drain.id}/enabled`, { enabled })
      drains.refresh()
    } catch (err) {
      notify.error(enabled ? "Could not resume the drain" : "Could not pause the drain", err)
    }
  }

  const remove = (drain: LogDrain) =>
    confirm({
      title: `Remove ${drain.name}`,
      confirmLabel: "Remove drain",
      description: (
        <p>
          Nothing more is sent to it. What it already delivered stays where it is, and what is
          waiting to be sent is dropped.
        </p>
      ),
      action: async () => {
        await del(`/deploy/log-drains/${drain.id}`)
      },
      onDone: () => drains.refresh(),
    })

  const verbsFor = (drain: LogDrain): Verb[] => {
    if (!admin) return []
    return [
      {
        key: "test",
        label: "Send test",
        icon: Lightning,
        inline: true,
        progressive: "Sending…",
        run: () => void test(drain),
      },
      { key: "edit", label: "Edit drain", icon: Pencil, run: () => open(draftOf(drain)) },
      drain.enabled
        ? { key: "pause", label: "Pause", icon: Pause, run: () => void setEnabled(drain, false) }
        : { key: "resume", label: "Resume", icon: Play, run: () => void setEnabled(drain, true) },
      { key: "remove", label: "Remove drain", icon: Trash, danger: true, run: () => remove(drain) },
    ]
  }

  const editing = draft?.id ? list?.find((drain) => drain.id === draft.id) : undefined

  return (
    <Page>
      <PageContext
        eyebrow={
          <Link href="/deploy" className="rounded-sm focus-ring hover:underline">
            Deployments
          </Link>
        }
        title="Log drains"
      />
      {drains.error && !list ? (
        <ErrorState error={drains.error} onRetry={drains.refresh} />
      ) : (
        <>
          {/* On the page's own ground, as Notifications' picture is. */}
          <div className="mx-auto w-full max-w-5xl lg:py-2">
            <DrainsPicture
              drains={list}
              projects={projects}
              loading={!list || fleet.waiting}
              onAdd={admin ? (kind) => open(emptyDraft(kind)) : undefined}
            />
          </div>
          <Panel plain>
            <PanelHeader
              title="Drains"
              actions={
                <span className="flex flex-wrap items-center gap-3">
                  {list && list.length > 0 && (
                    <span className="numeric text-hint text-muted-foreground">
                      {drainsCount(list)}
                    </span>
                  )}
                  {admin && list && list.length > 0 && (
                    <Button size="sm" onClick={() => open(emptyDraft("otlp"))}>
                      <Plus className="size-3.5" />
                      Add drain
                    </Button>
                  )}
                </span>
              }
            />
            <PanelBody>
              {!list ? (
                <LoadingRows rows={2} className="[&>*]:h-14 [&>*]:rounded-xl" />
              ) : list.length === 0 ? (
                admin ? (
                  // The empty page is the sheet's first question: which format.
                  <ChoiceGrid columns={3} className="animate-rise">
                    {KINDS.map((option, index) => (
                      <ChoiceCard
                        key={option.kind}
                        verb={`Add ${option.title}`}
                        logo={<ProductLogo id={DRAIN_PRODUCT[option.kind]} />}
                        title={option.title}
                        description={option.hint}
                        index={index}
                        onClick={() => open(emptyDraft(option.kind))}
                      />
                    ))}
                  </ChoiceGrid>
                ) : (
                  <EmptyNote>No log drains</EmptyNote>
                )
              ) : (
                <ChoiceList aria-label="Log drains" className="animate-rise">
                  {list.map((drain) => (
                    <DrainRow
                      key={drain.id}
                      drain={drain}
                      projects={projects}
                      wide={wide}
                      verbs={verbsFor(drain)}
                      onEdit={admin ? () => open(draftOf(drain)) : undefined}
                    />
                  ))}
                </ChoiceList>
              )}
            </PanelBody>
          </Panel>
        </>
      )}

      <SidePanel
        open={Boolean(draft)}
        onOpenChange={(next) => !next && close()}
        title={draft?.id ? "Edit drain" : "Add drain"}
        description="Copies what deployments say — their containers' output and the requests they served — to a log service, while it happens."
        width="md"
        footer={
          <>
            <Button variant="outline" onClick={close} disabled={saving}>
              Cancel
            </Button>
            <Button pending={saving} disabled={!draft || !ready(draft)} onClick={save}>
              {draft?.id ? "Save drain" : "Add drain"}
            </Button>
          </>
        }
      >
        {draft && (
          <DrainForm
            draft={draft}
            onChange={setDraft}
            saved={editing}
            projects={projects}
            formError={formError}
          />
        )}
      </SidePanel>
      {dialog}
    </Page>
  )
}

function ready(draft: Draft) {
  if (!draft.name.trim() || !draft.endpoint.trim()) return false
  const traces = draft.traces && draft.kind === "otlp"
  if (!draft.output && !draft.requests && !traces) return false
  if (traces && traceLinkProblem(draft.traceUrl)) return false
  if (!draft.everyProject && draft.projects.length === 0) return false
  const headers = draft.headers?.filter((header) => header.name.trim() || header.value) ?? []
  if (headers.some((header) => !header.name.trim() || !header.value)) return false
  return (
    drainEndpointProblem(
      draft.endpoint,
      headers.length > 0 || Boolean(draft.id && !draft.headers),
    ) !== "credential-over-http"
  )
}

/**
 * Where the logs go: the projects on one side, this server in the middle (it
 * is what reads every container's output and every request the ingress
 * served) and a mark for each drain on the other, each the service it sends
 * to. It is Notifications' picture with records in place of messages.
 *
 * The line to a drain carries a pulse while its last delivery arrived, stands
 * amber and still while the destination refuses, dashed while it is paused.
 * The formats not added yet are their marks in dashed rings, pressed to add
 * one by an administrator; before there is any drain one ring stands where
 * the first will go, and the cards under the picture ask which format.
 * Pointing at a drain steps back the projects it does not carry.
 *
 * On a phone the list under it names every drain, so the picture is the
 * marks alone: the projects over this server over the drains.
 */
function DrainsPicture({
  drains,
  projects,
  loading,
  onAdd,
}: {
  drains: LogDrain[] | undefined
  projects: DeploymentSummary[]
  loading: boolean
  onAdd?: (kind: LogDrainKind) => void
}) {
  const list = drains ?? NO_DRAINS
  const related = useCallback(
    (focus: string, id: string) => {
      const takes = (drainId: string, projectId: string) => {
        const drain = list.find((candidate) => drainNodeId(candidate.id) === drainId)
        const project = projects.find((candidate) => projectNodeId(candidate.id) === projectId)
        return Boolean(drain && project && drain.enabled && drainTakes(drain, project.id))
      }
      return takes(focus, id) || takes(id, focus)
    },
    [list, projects],
  )
  const copying = list.filter((drain) => drain.enabled)
  const sources: WireFlowNode[] =
    projects.length === 0
      ? [noProjectNode("each is copied once it deploys")]
      : projects.map((project) => {
          const taking = copying.filter((drain) => drainTakes(drain, project.id)).length
          return projectNode({
            id: project.id,
            name: project.name,
            deployment: project,
            hint: taking === 0 ? "not copied" : `to ${plural(taking, "drain")}`,
          })
        })
  const missing = KINDS.filter((kind) => !list.some((drain) => drain.kind === kind.kind)).slice(
    0,
    Math.max(0, PICTURE_TARGETS - list.length),
  )
  const targets: WireFlowNode[] =
    list.length === 0
      ? [
          {
            id: "drain:none",
            placeholder: true,
            mark: (size) => (
              <WirePlaceholder size={size}>
                <Plus />
              </WirePlaceholder>
            ),
            title: <span className="font-normal text-muted-foreground">No drain yet</span>,
            name: "No drain yet",
            hint: "the first one goes here",
            line: { state: "idle" },
          },
        ]
      : [
          ...list.map((drain): WireFlowNode => {
            const state = drainStateWord(drain).label.toLowerCase()
            return {
              id: drainNodeId(drain.id),
              mark: (size) => (
                <WireMark
                  tone="logo"
                  size={size}
                  className={cn(!drain.enabled && "opacity-50 grayscale")}
                >
                  <ProductGlyph id={DRAIN_PRODUCT[drain.kind]} />
                </WireMark>
              ),
              eyebrow: KIND_TITLE[drain.kind],
              title: drain.name,
              name: `${drain.name}: ${state}`,
              hint: state,
              line: drainLine(drain),
            }
          }),
          ...missing.map((kind): WireFlowNode => ({
            id: `add:${kind.kind}`,
            placeholder: true,
            mark: (size) => <WirePlaceholder size={size} product={DRAIN_PRODUCT[kind.kind]} />,
            eyebrow: "Not added",
            title: <span className="text-muted-foreground">{kind.title}</span>,
            name: kind.title,
            hint: kind.short,
            onPress: onAdd ? () => onAdd(kind.kind) : undefined,
            label: `Add ${kind.title}`,
            line: { state: "idle" },
          })),
        ]
  return (
    <WireFlow
      className="animate-rise"
      phone="marks"
      loading={loading}
      related={related}
      start={{
        title: "Projects copied",
        nodes: sources,
        max: 3,
        more: { href: "/deploy", words: (hidden) => plural(hidden, "more project") },
      }}
      hub={{
        mark: (size) => <WireHost size={size} />,
        eyebrow: "This server",
        title: "Deployments' logs",
        hint:
          copying.length === 0
            ? "copied nowhere yet"
            : `copied to ${plural(copying.length, "drain")}`,
      }}
      end={{
        title: "Where the logs go",
        nodes: targets,
        max: PICTURE_TARGETS,
        more: { words: (hidden) => plural(hidden, "more drain") },
        loadingRows: 3,
      }}
    />
  )
}

const NO_DRAINS: LogDrain[] = []
const drainNodeId = (id: number) => `drain:${id}`

/**
 * One drain on one line: the service it sends to, its name over the host and
 * what it carries, then how it is doing as a dot and a word, and one figure.
 * Every state is the same height: the destination's own words, which were a
 * second line under a failing drain, open from the state word.
 *
 * A phone has no room for the state and the figure beside the name, and
 * keeps them on a second line under it, on every row alike.
 */
function DrainRow({
  drain,
  projects,
  wide,
  verbs,
  onEdit,
}: {
  drain: LogDrain
  projects: DeploymentSummary[]
  wide: boolean
  verbs: Verb[]
  onEdit?: () => void
}) {
  let host = drain.endpoint
  try {
    host = new URL(drain.endpoint).host
  } catch {
    // Shown as typed.
  }
  const reach = drainReach(drain, projects)
  const state = <DrainStatus drain={drain} />
  const figure = drainFigure(drain)
  const reading = (
    <span className={cn("numeric text-hint whitespace-nowrap", figure.warning && "text-warning")}>
      {figure.text}
    </span>
  )
  return (
    <ChoiceRow
      leading={
        <ProductLogo
          id={DRAIN_PRODUCT[drain.kind]}
          size="sm"
          className={cn(!drain.enabled && "opacity-50 grayscale")}
        />
      }
      title={drain.name}
      verb={`Edit ${drain.name}`}
      onSelect={onEdit}
      disabled={!onEdit}
      description={
        <>
          <span className="font-mono">{host}</span>
          {wide && ` · ${reach}`}
        </>
      }
      trailing={
        wide && (
          // Fixed widths, so down a list the states are one column and the
          // figures end on one edge.
          <span className="flex items-center gap-3 text-muted-foreground">
            <span className="flex w-28">{state}</span>
            <span className="flex w-28 justify-end">{reading}</span>
          </span>
        )
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${drain.name}`} />}
    >
      {!wide && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2 text-hint text-muted-foreground">
          <div className="basis-full">{state}</div>
          <span className="min-w-0 flex-1 truncate">{reach}</span>
          {reading}
        </div>
      )}
    </ChoiceRow>
  )
}

/**
 * How a drain is doing, as a dot and a word that open on the rest: the
 * destination's answer in its own words, what was shipped, dropped and is
 * waiting, and the address in full. It was a second line under the name,
 * which made a failing drain a taller card than a healthy one.
 */
function DrainStatus({ drain }: { drain: LogDrain }) {
  const word = drainStateWord(drain)
  const health = drainHealth(drain)
  const { status } = drain
  const facts: [string, string][] = [
    ["Shipped", plural(status.shipped, "record")],
    ["Dropped", plural(status.dropped, "record")],
    ["Waiting", plural(status.queued, "record")],
    ["Deliveries", status.batches.toLocaleString()],
    ["Last delivery", status.lastSuccessAt ? relativeTime(status.lastSuccessAt) : "never"],
  ]
  if (drain.headerNames.length > 0) facts.push(["Sends", `${drain.headerNames.join(", ")}, sealed`])
  return (
    <Popover>
      <PopoverTrigger
        aria-label={`${word.label}: how ${drain.name} is doing`}
        className="rounded-sm focus-ring"
      >
        <Status tone={word.tone} label={word.label} />
      </PopoverTrigger>
      {/* A press in here reaches the row through React even from a portal, and
          the row would open its editor. */}
      <PopoverContent align="end" className="w-80" onClick={(event) => event.stopPropagation()}>
        <div className="space-y-3 text-hint">
          <p className={cn("break-words", health.tone === "warning" && "text-warning")}>
            {health.line}
          </p>
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1">
            {facts.map(([name, value]) => (
              <div key={name} className="contents">
                <dt className="text-muted-foreground">{name}</dt>
                <dd className="numeric min-w-0 text-right break-words">{value}</dd>
              </div>
            ))}
          </dl>
          <p className="font-mono break-all text-muted-foreground">
            {KIND_TITLE[drain.kind]} · {drain.endpoint}
          </p>
        </div>
      </PopoverContent>
    </Popover>
  )
}

/**
 * The sheet: the format first (it decides the address and the header a
 * service expects), then where and with what credential, then what to send.
 */
function DrainForm({
  draft,
  onChange,
  saved,
  projects,
  formError,
}: {
  draft: Draft
  onChange: (draft: Draft) => void
  saved?: LogDrain
  projects: DeploymentSummary[]
  formError?: string
}) {
  const patch = (fields: Partial<Draft>) => onChange({ ...draft, ...fields })
  const kind = KINDS.find((option) => option.kind === draft.kind) ?? KINDS[0]
  const headers = draft.headers
  const sending = headers
    ? headers.some((header) => header.name.trim() && header.value)
    : Boolean(saved?.headerNames.length)
  const problem = drainEndpointProblem(draft.endpoint, sending)
  const setHeader = (index: number, fields: Partial<DrainHeaderDraft>) =>
    patch({
      headers: (headers ?? []).map((header, at) =>
        at === index ? { ...header, ...fields } : header,
      ),
    })
  return (
    <div className="flex flex-col gap-6">
      <fieldset className="min-w-0">
        <legend className="mb-1.5 text-body font-medium">Format</legend>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
          {KINDS.map((option) => {
            const selected = draft.kind === option.kind
            return (
              <ChoiceCard
                key={option.kind}
                selected={selected}
                className="min-h-0 flex-row items-start gap-2.5 px-3 py-2.5"
                onClick={() => patch({ kind: option.kind })}
              >
                <KindMark
                  kind={option.kind}
                  className={cn(
                    "mt-px size-4 shrink-0",
                    selected ? "text-brand" : "text-muted-foreground",
                  )}
                />
                <span className="flex min-w-0 flex-col gap-0.5">
                  <ChoiceCardTitle>{option.title}</ChoiceCardTitle>
                  <ChoiceCardHint className="line-clamp-2">{option.short}</ChoiceCardHint>
                </span>
              </ChoiceCard>
            )
          })}
        </div>
        <FormNote className="mt-2">{kind.hint}</FormNote>
      </fieldset>

      <FormSection title="Destination">
        <Field label="Name" htmlFor="drain-name">
          <Input
            id="drain-name"
            value={draft.name}
            onChange={(event) => patch({ name: event.target.value })}
            placeholder={kind.title}
          />
        </Field>
        <Field label="Endpoint" htmlFor="drain-endpoint" hint={ENDPOINT_HINT[draft.kind]}>
          <Input
            id="drain-endpoint"
            value={draft.endpoint}
            onChange={(event) => patch({ endpoint: event.target.value })}
            placeholder={kind.placeholder}
            spellCheck={false}
            className="font-mono text-body"
          />
        </Field>
        {problem === "credential-over-http" ? (
          <FormNote tone="warning">
            A header goes over https, or to this machine; this address would send it in the clear.
          </FormNote>
        ) : problem === "cleartext" ? (
          <FormNote>
            Plain http: records — client addresses and whatever the application prints — cross the
            network unencrypted.
          </FormNote>
        ) : null}
        {headers ? (
          <div className="flex flex-col gap-2">
            {headers.map((header, index) => (
              <FieldRow key={index}>
                <Field label={index === 0 ? "Header" : undefined} htmlFor={`drain-header-${index}`}>
                  <Input
                    id={`drain-header-${index}`}
                    aria-label={`Header ${index + 1} name`}
                    value={header.name}
                    onChange={(event) => setHeader(index, { name: event.target.value })}
                    placeholder="Authorization"
                    spellCheck={false}
                  />
                </Field>
                <Field
                  label={index === 0 ? "Value" : undefined}
                  htmlFor={`drain-header-value-${index}`}
                >
                  <Input
                    id={`drain-header-value-${index}`}
                    aria-label={`Header ${index + 1} value`}
                    type="password"
                    value={header.value}
                    onChange={(event) => setHeader(index, { value: event.target.value })}
                    placeholder="Bearer …"
                    spellCheck={false}
                  />
                </Field>
              </FieldRow>
            ))}
            <span className="flex flex-wrap items-center gap-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  patch({
                    headers: [
                      ...headers,
                      {
                        name:
                          SUGGESTED_HEADERS[draft.kind].find(
                            (name) => !headers.some((header) => header.name === name),
                          ) ?? "",
                        value: "",
                      },
                    ],
                  })
                }
              >
                <Plus className="size-3.5" />
                Add header
              </Button>
              <span className="text-hint text-muted-foreground">
                Sealed with the dashboard&apos;s master key and never shown again.
              </span>
            </span>
          </div>
        ) : (
          <FormNote>
            {saved?.headerNames.length ? (
              <>
                Sends {saved.headerNames.join(", ")}, sealed.{" "}
                <Button
                  variant="link"
                  size="sm"
                  className="h-auto p-0"
                  onClick={() => patch({ headers: [{ name: "", value: "" }] })}
                >
                  Replace headers
                </Button>
              </>
            ) : (
              <>
                Sends no header.{" "}
                <Button
                  variant="link"
                  size="sm"
                  className="h-auto p-0"
                  onClick={() =>
                    patch({ headers: [{ name: SUGGESTED_HEADERS[draft.kind][0], value: "" }] })
                  }
                >
                  Add one
                </Button>
              </>
            )}
          </FormNote>
        )}
      </FormSection>

      <FormSection title="What to send">
        <OptionList>
          <OptionRow
            title="Container output"
            hint="Every line the deployment's containers write, with the stream and container it came from."
            checked={draft.output}
            onCheckedChange={(output) => patch({ output })}
          />
          <OptionRow
            title="Requests"
            hint="Each request the ingress served: method, path, status, duration and client."
            checked={draft.requests}
            onCheckedChange={(requests) => patch({ requests })}
          />
          {draft.kind === "otlp" && (
            <OptionRow
              title="Traces"
              hint="Each request again as a span on /v1/traces — the deployment as its service, the method and route as its name, the status, client and release beside it — under the trace the ingress sends the application, so its own spans join it."
              checked={draft.traces}
              onCheckedChange={(traces) => patch({ traces })}
            >
              <Field
                label="Trace link"
                htmlFor="drain-trace-url"
                info="Where your tracing service opens a trace, with {traceId} where the id goes. The request console draws it beside each request's trace."
                error={traceLinkProblem(draft.traceUrl)}
              >
                <Input
                  id="drain-trace-url"
                  value={draft.traceUrl}
                  placeholder="https://grafana.example.com/explore?traceId={traceId}"
                  className="font-mono"
                  onChange={(event) => patch({ traceUrl: event.target.value })}
                />
              </Field>
            </OptionRow>
          )}
          <OptionRow
            title="Every project"
            hint="Including the ones created after this drain."
            checked={draft.everyProject}
            onCheckedChange={(everyProject) => patch({ everyProject })}
          />
        </OptionList>
        {!draft.everyProject && (
          <OptionList aria-label="Projects">
            {projects.map((project) => (
              <OptionRow
                key={project.id}
                title={
                  <span className="inline-flex items-center gap-2">
                    <ProjectMark deployment={project} size="xs" />
                    {project.name}
                  </span>
                }
                checked={draft.projects.includes(project.id)}
                onCheckedChange={(on) =>
                  patch({
                    projects: on
                      ? [...draft.projects, project.id]
                      : draft.projects.filter((id) => id !== project.id),
                  })
                }
              />
            ))}
          </OptionList>
        )}
        <OptionList>
          <OptionRow
            title="Enabled"
            hint="A paused drain keeps its settings and sends nothing."
            checked={draft.enabled}
            onCheckedChange={(enabled) => patch({ enabled })}
          />
        </OptionList>
      </FormSection>
      {formError && (
        <FormNote tone="danger" role="alert">
          {formError}
        </FormNote>
      )}
    </div>
  )
}

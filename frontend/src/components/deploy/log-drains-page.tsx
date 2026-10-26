"use client"

import { useState } from "react"
import Link from "next/link"
import {
  ChartActivity,
  CodeBracket,
  Lightning,
  Pause,
  Pencil,
  Play,
  Plus,
  Trash,
  type Icon,
} from "@/components/icons"
import { ApiError, del, get, post, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentFleet,
  DeploymentSummary,
  LogDrain,
  LogDrainKind,
  LogDrainStream,
} from "@/lib/types"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { SidePanel } from "@/components/side-panel"
import { Tag } from "@/components/tag"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FieldRow, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { ProductGlyph } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { VerbActions, type Verb } from "@/components/verbs"
import { ProjectMark } from "@/components/deploy/project-mark"
import {
  drainEndpointProblem,
  drainHealth,
  drainReach,
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

// Grafana as a glyph the cards draw beside their titles like the others' marks.
function GrafanaMark({ className }: { className?: string }) {
  return <ProductGlyph id="grafana" className={className} />
}

const KIND_MARK: Record<LogDrainKind, Icon | typeof GrafanaMark> = {
  otlp: ChartActivity,
  loki: GrafanaMark,
  http: CodeBracket,
}

function KindMark({ kind, className }: { kind: LogDrainKind; className?: string }) {
  if (kind === "loki") return <ProductGlyph id="grafana" className={className} />
  const Glyph: Icon = kind === "otlp" ? ChartActivity : CodeBracket
  return <Glyph aria-hidden className={className} />
}

const ENDPOINT_HINT: Record<LogDrainKind, string> = {
  otlp: "The OTLP base address, as an exporter takes it: /v1/logs is added unless it is already there.",
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
    everyProject: drain.projects.length === 0,
    projects: drain.projects,
  }
}

/**
 * Log drains: where deployments' output and requests are copied while they
 * happen. A card per drain says where it sends, what, and how its last
 * delivery went — in amber the moment a destination stops taking records,
 * with the destination's own words. The sheet asks for the format, the
 * address and the credential, then what to send.
 */
export function LogDrainsPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const drains = usePoll(
    (signal) => get<LogDrain[]>("/deploy/log-drains", undefined, signal),
    5000,
    [],
  )
  const fleet = usePoll(
    (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
    60000,
    [],
  )
  const projects = fleet.data?.deployments ?? []
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
    const body = {
      name: draft.name.trim(),
      kind: draft.kind,
      endpoint: draft.endpoint.trim(),
      enabled: draft.enabled,
      streams,
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
      <Panel plain>
        <PanelHeader
          title="Drains"
          actions={
            <span className="flex flex-wrap items-center gap-3">
              {list && list.length > 0 && (
                <span className="numeric text-hint text-muted-foreground">
                  {plural(list.length, "drain")}
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
          {drains.error ? (
            <ErrorState error={drains.error} onRetry={drains.refresh} />
          ) : drains.loading && !list ? (
            <LoadingRows rows={2} />
          ) : (list?.length ?? 0) === 0 ? (
            admin ? (
              // The empty page is the sheet's first question: which format.
              <ChoiceGrid columns={3} className="animate-rise">
                {KINDS.map((option, index) => (
                  <ChoiceCard
                    key={option.kind}
                    verb={`Add ${option.title}`}
                    mark={KIND_MARK[option.kind]}
                    title={option.title}
                    description={option.hint}
                    index={index}
                    onClick={() => open(emptyDraft(option.kind))}
                  />
                ))}
              </ChoiceGrid>
            ) : (
              <EmptyNote>No drain copies deployments&apos; logs anywhere yet.</EmptyNote>
            )
          ) : (
            <ChoiceList aria-label="Log drains" className="animate-rise">
              {list?.map((drain) => (
                <DrainCard
                  key={drain.id}
                  drain={drain}
                  projects={projects}
                  verbs={verbsFor(drain)}
                  onEdit={admin ? () => open(draftOf(drain)) : undefined}
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

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
  if (!draft.output && !draft.requests) return false
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
 * One drain: its format's mark, its name, and on one line where it sends,
 * what, and how the last delivery went. Paused drains say so before
 * anything else, and a failing one says what the destination answered.
 */
function DrainCard({
  drain,
  projects,
  verbs,
  onEdit,
}: {
  drain: LogDrain
  projects: DeploymentSummary[]
  verbs: Verb[]
  onEdit?: () => void
}) {
  const health = drainHealth(drain)
  let host = drain.endpoint
  try {
    host = new URL(drain.endpoint).host
  } catch {
    // Shown as typed.
  }
  return (
    <ChoiceRow
      leading={<KindMark kind={drain.kind} className="size-5" />}
      title={drain.name}
      verb={`Edit ${drain.name}`}
      onSelect={onEdit}
      disabled={!onEdit}
      description={
        <span className="flex min-w-0 flex-col gap-0.5">
          <span>
            <span className="font-mono">{host}</span> · {drainReach(drain, projects)}
          </span>
          <span className={health.tone === "warning" ? "text-warning" : undefined}>
            {health.line}
          </span>
        </span>
      }
      trailing={<Tag>{KIND_TITLE[drain.kind]}</Tag>}
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${drain.name}`} />}
    />
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

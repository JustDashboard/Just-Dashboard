"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  CheckCircle,
  FileText,
  Globe,
  Lightning,
  RotateClockwise,
  Servers,
  SettingsSliders,
  Shield,
  Wrench,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  ContainerDetail,
  ContainerSpec,
  CreateResult,
  DockerFinding,
  FindingClass,
  SpecPreview,
} from "@/lib/types"
import {
  changedSpecFields,
  composeRemedy,
  DOCKER_REMEDIES,
  dockerRemedyKind,
  prepareDockerRemedy,
  type DockerRemedyKind,
} from "@/lib/docker-remedies"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { ProductGlyph } from "@/components/product-logo"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Button } from "@/components/ui/button"
import { BorderBeam } from "@/components/ui/border-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { CODE } from "@/components/deploy/run-evidence"
import { restartWords } from "@/components/docker/container"
import type { ConfirmFn } from "./shared"

/**
 * A container's configuration, and what to change in it.
 *
 * The 2026-10-08 overhaul took the Health page's shape (§15): it was, for a
 * compose container, one grey notice of underlined links, and for a standalone
 * one a list of remedies in prose over a bare JSON box with "Changed fields:"
 * as a sentence under it. Now each finding this tab can act on is a lit card
 * on its level's wash — the thing you take (§16) — opening its fix in place;
 * the settings those fixes touch stand under them as what they are now, in
 * the finding's tone where one points at them; and the replacement is one
 * framed editor whose head names every field the draft changes in the hue a
 * pending change takes (§3), with a light running round it while Docker
 * replaces the container.
 */
export function ConfigurationRemedy({
  detail,
  findings,
  confirm,
  onChanged,
}: {
  detail: ContainerDetail
  findings: DockerFinding[]
  confirm: ConfirmFn
  onChanged: () => void
}) {
  const { can } = useAuth()
  if (detail.composeStack) {
    return <ComposeRemedy detail={detail} findings={findings} />
  }
  if (!can("system.admin")) {
    return (
      <div className="flex min-w-0 flex-col gap-8 pb-4">
        <FindingCards findings={findings} detail={detail} />
        <CurrentSettings detail={detail} findings={findings} />
        <Notice title="Administrator required">
          The replacement specification includes environment credentials and host mounts. You can
          review the evidence under Inspect; an administrator can edit and apply this configuration.
        </Notice>
      </div>
    )
  }
  return (
    <StandaloneRemedy
      key={detail.id}
      detail={detail}
      findings={findings}
      confirm={confirm}
      onChanged={onChanged}
    />
  )
}

/** A finding's severity on the Health page's three levels, which the cards are drawn in. */
type Level = "critical" | "warning" | "notice"

const LEVEL_GROUND: Record<Level, string> = {
  critical: "bg-wash-danger",
  warning: "bg-wash-warning",
  notice: "",
}

const LEVEL_BAR: Record<Level, string> = {
  critical: "bg-destructive",
  warning: "bg-warning",
  notice: "bg-muted-foreground/50",
}

const LEVEL_TEXT: Record<Level, string> = {
  critical: "text-destructive",
  warning: "text-warning",
  notice: "text-muted-foreground",
}

function levelOf(finding: DockerFinding): Level {
  if (finding.severity === "critical") return "critical"
  if (finding.severity === "warning") return "warning"
  return "notice"
}

/** What kind of problem it is, as the glyph beside the level's bar. */
const CLASS_GLYPH: Record<FindingClass, React.ComponentType<{ className?: string }>> = {
  security: Shield,
  exposure: Globe,
  configuration: SettingsSliders,
  storage: Servers,
  runtime: Lightning,
  lifecycle: RotateClockwise,
}

/** The finding's kind, the segment of its id every remedy table is keyed by. */
const kindOf = (finding: DockerFinding) => finding.id.split(".")[1] ?? ""

/**
 * What the compose file is edited for, where the finding has a snippet: the
 * five remedies, a memory limit, and the two the Overview fixes in place on a
 * standalone container (a restart policy, capped logs), which on a compose
 * container belong in its service like the rest.
 */
function composeKind(finding: DockerFinding): string | undefined {
  const kind = kindOf(finding)
  if (dockerRemedyKind(finding) || kind === "nomemorylimit") return kind
  if (finding.action === "set-restart" || finding.action === "cap-logs") return finding.action
  return undefined
}

/** The level as a bar of its hue beside the class's glyph in the same hue, as Health draws it. */
function SeverityMark({ finding }: { finding: DockerFinding }) {
  const level = levelOf(finding)
  const Icon = CLASS_GLYPH[finding.class] ?? SettingsSliders
  return (
    <span className="flex items-center gap-2.5">
      <span aria-hidden className={cn("h-7 w-0.5 rounded-full", LEVEL_BAR[level])} />
      <Icon className={cn("size-4", LEVEL_TEXT[level])} />
    </span>
  )
}

/** Worst first, so the card that matters most is the first one read. */
function worstFirst(findings: DockerFinding[]) {
  const rank: Record<Level, number> = { critical: 0, warning: 1, notice: 2 }
  return [...findings].sort((a, b) => rank[levelOf(a)] - rank[levelOf(b)])
}

/** With nothing to change, one line in the success hue, the way a healthy host reads. */
function Settled({ children }: { children: React.ReactNode }) {
  return (
    <p className="flex min-w-0 animate-rise items-center gap-2.5 rounded-xl border border-rule-success bg-wash-success px-3 py-2.5 text-body">
      <CheckCircle className="size-4 shrink-0 text-success" />
      {children}
    </p>
  )
}

/**
 * The findings, as cards that cannot be taken: for an operator who may not
 * replace the container, or a fix that lives on the Overview. A card that
 * looks pressable and does nothing is the defect `disabled` exists to stop.
 */
function FindingCards({
  findings,
  detail,
}: {
  findings: DockerFinding[]
  detail: ContainerDetail
}) {
  return (
    <Panel plain>
      <PanelHeader title="What to change" actions={<FindingCount findings={findings} />} />
      <PanelBody className="pt-3">
        {findings.length === 0 ? (
          <Settled>No configuration change is suggested for {detail.name}.</Settled>
        ) : (
          <ChoiceList>
            {worstFirst(findings).map((finding, index) => (
              <ChoiceRow
                key={finding.id}
                index={index}
                disabled
                verb={finding.title}
                className={LEVEL_GROUND[levelOf(finding)]}
                leading={<SeverityMark finding={finding} />}
                title={finding.title}
                description={finding.detail}
              />
            ))}
          </ChoiceList>
        )}
      </PanelBody>
    </Panel>
  )
}

function FindingCount({ findings }: { findings: DockerFinding[] }) {
  if (findings.length === 0) return null
  const serious = findings.filter((finding) => levelOf(finding) !== "notice").length
  return (
    <span className="numeric text-hint text-muted-foreground">
      {serious > 0 ? (
        <>
          <span className="font-medium text-warning">{serious}</span> to fix ·{" "}
        </>
      ) : null}
      {findings.length} in all
    </span>
  )
}

/**
 * The settings the remedies touch, as they are now. A figure on a page you
 * configure is best drawn beside the control that sets it (§15 pass 2); the
 * control here is the card above, so the value stands under it, in the tone
 * of the finding that points at it.
 */
function CurrentSettings({
  detail,
  findings,
}: {
  detail: ContainerDetail
  findings: DockerFinding[]
}) {
  const flagged = (...kinds: string[]): Level | undefined => {
    const hits = findings.filter(
      (finding) =>
        kinds.includes(kindOf(finding)) || (finding.action && kinds.includes(finding.action)),
    )
    if (hits.length === 0) return undefined
    return hits.some((finding) => levelOf(finding) === "critical")
      ? "critical"
      : hits.some((finding) => levelOf(finding) === "warning")
        ? "warning"
        : "notice"
  }
  const published = (detail.exposure ?? [])
    .filter((port) => port.hostPort !== undefined)
    .map((port) =>
      port.scope === "all" || !port.hostIp
        ? `${port.hostPort} → ${port.containerPort}`
        : `${port.hostIp}:${port.hostPort} → ${port.containerPort}`,
    )
  const socket = detail.mounts.some((mount) => /(^|\/)docker\.sock$/.test(mount.source))
  const rows: { label: string; value: React.ReactNode; mono?: boolean; level?: Level }[] = [
    { label: "Image", value: detail.image, mono: true, level: flagged("latest") },
    {
      label: "Restart policy",
      value: restartWords(detail.restartPolicy),
      level: flagged("norestart", "set-restart"),
    },
    {
      label: "Health check",
      value: detail.hasHealthcheck ? "checked by Docker" : "none",
      level: flagged("nohealthcheck"),
    },
    {
      label: "Memory limit",
      value: detail.memoryLimit ? bytes(detail.memoryLimit) : "no limit",
      level: flagged("nomemorylimit"),
    },
    {
      label: "CPU limit",
      value: detail.cpuLimit
        ? `${detail.cpuLimit} ${detail.cpuLimit === 1 ? "core" : "cores"}`
        : "no limit",
    },
    {
      label: "Published ports",
      value: published.length > 0 ? published.join(", ") : "none",
      mono: published.length > 0,
      level: flagged("exposed"),
    },
    {
      label: "Privileged",
      value: detail.privileged ? "yes — full host access" : "no",
      level: flagged("privileged") ?? (detail.privileged ? "critical" : undefined),
    },
    {
      label: "Docker socket",
      value: socket ? "mounted — it can control Docker" : "not mounted",
      level: flagged("dockersock") ?? (socket ? "critical" : undefined),
    },
  ]
  return (
    <Panel plain className="animate-rise">
      <PanelHeader title="What it is set to" />
      <PanelBody>
        <dl className="grid grid-cols-1 gap-x-8 sm:grid-cols-2">
          {rows.map((row) => (
            <div
              key={row.label}
              className="flex min-w-0 items-baseline justify-between gap-3 border-b border-hairline py-2 text-body"
            >
              <dt className="flex shrink-0 items-center gap-2 text-muted-foreground">
                {/* The finding's bar, small, so the rows a card above points
                    at are found down the column without reading them. */}
                <span
                  aria-hidden
                  className={cn(
                    "h-3 w-0.5 rounded-full",
                    row.level ? LEVEL_BAR[row.level] : "bg-transparent",
                  )}
                />
                {row.label}
              </dt>
              <dd
                className={cn(
                  "min-w-0 truncate text-right font-medium",
                  row.mono && "font-mono text-xs",
                  row.level && row.level !== "notice" && LEVEL_TEXT[row.level],
                )}
                title={typeof row.value === "string" ? row.value : undefined}
              >
                {row.value}
              </dd>
            </div>
          ))}
        </dl>
      </PanelBody>
    </Panel>
  )
}

/**
 * A snippet of compose YAML with its keys, values, placeholders and comments
 * in the hues a command's words take (`CODE`), so the part to fill in is
 * found before it is read.
 */
function YamlSnippet({ text }: { text: string }) {
  return (
    <>
      {text.split("\n").map((line, index) => {
        const comment = line.indexOf("#")
        const code = comment === -1 ? line : line.slice(0, comment)
        const note = comment === -1 ? "" : line.slice(comment)
        const match = code.match(/^(\s*-?\s*)([A-Za-z_][\w.-]*)(:)(.*)$/)
        return (
          <span key={index} className="block">
            {match ? (
              <>
                {match[1]}
                <span className={CODE.key}>{match[2]}</span>
                <span className={CODE.punct}>{match[3]}</span>
                <YamlValue text={match[4]} />
              </>
            ) : (
              <YamlValue text={code} />
            )}
            {note && <span className={CODE.comment}>{note}</span>}
          </span>
        )
      })}
    </>
  )
}

function YamlValue({ text }: { text: string }) {
  return (
    <>
      {text.split(/(<[^>]+>|"[^"]*"|'[^']*'|\b\d+(?:\.\d+)?[a-z]*\b)/).map((part, index) =>
        part.startsWith("<") ? (
          <span key={index} className={CODE.variable}>
            {part}
          </span>
        ) : /^["']/.test(part) ? (
          <span key={index} className={CODE.string}>
            {part}
          </span>
        ) : /^\d/.test(part) ? (
          <span key={index} className={CODE.number}>
            {part}
          </span>
        ) : (
          part
        ),
      )}
    </>
  )
}

/**
 * A compose container: every change belongs in its service, because the next
 * deploy silently undoes one made to the container, days later. So each card
 * carries the snippet that goes into the service and opens the compose editor
 * on it, and the owner is said as a fact rather than as a warning.
 */
function ComposeRemedy({
  detail,
  findings,
}: {
  detail: ContainerDetail
  findings: DockerFinding[]
}) {
  const stack = detail.composeStack!
  const owned = worstFirst(findings.filter((finding) => composeKind(finding)))
  const editor = (kind?: string) =>
    `/docker/stacks/${encodeURIComponent(stack)}?tab=compose${
      kind ? `&remedy=${encodeURIComponent(kind)}` : ""
    }`
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-4">
      <Panel plain>
        <PanelHeader title="What to change" actions={<FindingCount findings={owned} />} />
        <PanelBody className="space-y-4 pt-3">
          <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2">
            <p className="flex min-w-0 items-center gap-2 text-body">
              <ProductGlyph id="docker-compose" className="size-4" />
              <span className="min-w-0">
                Compose owns it as{" "}
                <span className="font-medium">
                  {stack}
                  {detail.composeService ? ` · ${detail.composeService}` : ""}
                </span>
                <span className="text-muted-foreground">
                  {" "}
                  — a change goes into its service, or the next deploy undoes it.
                </span>
              </span>
            </p>
            <Button size="xs" variant="outline" asChild>
              <Link href={editor()}>
                <FileText className="size-3" />
                Open Compose file
              </Link>
            </Button>
          </div>
          {owned.length === 0 ? (
            <Settled>No configuration change is suggested for {detail.name}.</Settled>
          ) : (
            <ChoiceList>
              {owned.map((finding, index) => {
                const kind = composeKind(finding)!
                return (
                  <ChoiceRow
                    key={finding.id}
                    index={index}
                    verb={`Edit in compose: ${finding.title}`}
                    href={editor(kind)}
                    className={LEVEL_GROUND[levelOf(finding)]}
                    leading={<SeverityMark finding={finding} />}
                    title={finding.title}
                    description={finding.detail}
                    trailing={
                      <span className="hidden text-hint font-medium sm:inline">
                        Edit in compose
                      </span>
                    }
                  >
                    <Well className="ml-9 text-xs leading-relaxed whitespace-pre-wrap">
                      <YamlSnippet text={composeRemedy(kind)} />
                    </Well>
                  </ChoiceRow>
                )
              })}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>
      <CurrentSettings detail={detail} findings={findings} />
    </div>
  )
}

function StandaloneRemedy({
  detail,
  findings,
  confirm,
  onChanged,
}: {
  detail: ContainerDetail
  findings: DockerFinding[]
  confirm: ConfirmFn
  onChanged: () => void
}) {
  const { can } = useAuth()
  const router = useRouter()
  const source = usePoll(
    (signal) => get<ContainerSpec>(`/docker/containers/${detail.id}/spec`, undefined, signal),
    0,
    [detail.id],
  )
  const [draft, setDraft] = useState<string>()
  const [values, setValues] = useState<Partial<Record<DockerRemedyKind, string>>>({})
  // The card whose field is open: the two remedies that need a value say so
  // in their own card rather than in a form under the list.
  const [open, setOpen] = useState<DockerRemedyKind>()
  const [prepared, setPrepared] = useState<Set<DockerRemedyKind>>(new Set())
  const [error, setError] = useState("")
  const [preview, setPreview] = useState<{
    spec: ContainerSpec
    content: string
    rendered: SpecPreview
  }>()
  const [busy, setBusy] = useState(false)
  const [replacing, setReplacing] = useState(false)
  const content = draft ?? (source.data ? JSON.stringify(source.data, null, 2) : "")
  const edit = (next: string) => {
    setDraft(next)
    setPreview(undefined)
    setError("")
  }
  const prepare = (kind: DockerRemedyKind) => {
    try {
      edit(JSON.stringify(prepareDockerRemedy(JSON.parse(content), kind, values[kind]), null, 2))
      setPrepared((held) => new Set(held).add(kind))
      setOpen(undefined)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }
  const choose = (kind: DockerRemedyKind) => {
    if (kind === "latest" || kind === "nohealthcheck") setOpen(open === kind ? undefined : kind)
    else prepare(kind)
  }
  const review = async () => {
    setBusy(true)
    setError("")
    try {
      const spec = JSON.parse(content) as ContainerSpec
      if (
        !spec ||
        Array.isArray(spec) ||
        typeof spec !== "object" ||
        !spec.image?.trim() ||
        !spec.limits
      )
        throw new Error("A specification with an image and limits is required")
      if (spec.name !== source.data?.name)
        throw new Error("Keep the original container name; use Rename separately")
      const rendered = await post<SpecPreview>("/docker/containers/preview", spec)
      setPreview({ spec, content, rendered })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const apply = () => {
    if (!preview || preview.content !== content || !source.data || source.error) return
    const spec = preview.spec
    confirm({
      title: `Replace ${detail.name}`,
      confirmLabel: "Replace container",
      description: `Changed fields: ${changedSpecFields(source.data, spec).join(", ")}. This starts a replacement and interrupts the service. Mounts and volumes in the reviewed specification remain; the old logs and data stored only in its writable layer are permanently lost. Move required data to a volume first. A running replacement does not prove application readiness.`,
      action: async () => {
        setReplacing(true)
        try {
          const result = await post<CreateResult>(`/docker/containers/${detail.id}/recreate`, {
            spec,
          })
          if (result.warnings?.length)
            notify.warning("Replacement started with caveats", {
              description: result.warnings.join("; "),
            })
          else notify.success("Replacement started; check its health and logs")
          onChanged()
          router.push(`/docker/containers/${encodeURIComponent(result.id)}`)
          return "reported"
        } finally {
          setReplacing(false)
        }
      },
    })
  }
  if (source.loading && !source.data) return <LoadingRows />
  if (source.error) return <ErrorState error={source.error} onRetry={source.refresh} />
  if (!source.data) return null
  let changed: string[] = []
  try {
    changed = changedSpecFields(source.data, JSON.parse(content))
  } catch {
    /* The editor reports invalid JSON when the draft is reviewed. */
  }
  const remedies = worstFirst(findings.filter((finding) => dockerRemedyKind(finding)))
  const elsewhere = worstFirst(findings.filter((finding) => !dockerRemedyKind(finding)))

  return (
    <div className="flex min-w-0 flex-col gap-8 pb-4">
      <Panel plain>
        <PanelHeader title="What to change" actions={<FindingCount findings={findings} />} />
        <PanelBody className="pt-3">
          {findings.length === 0 ? (
            <Settled>No configuration change is suggested for {detail.name}.</Settled>
          ) : (
            <ChoiceList>
              {remedies.map((finding, index) => {
                const kind = dockerRemedyKind(finding)!
                const done = prepared.has(kind)
                const asking = open === kind
                return (
                  <ChoiceRow
                    key={finding.id}
                    index={index}
                    verb={`${DOCKER_REMEDIES[kind].label}: ${finding.title}`}
                    onSelect={() => choose(kind)}
                    className={LEVEL_GROUND[levelOf(finding)]}
                    leading={<SeverityMark finding={finding} />}
                    title={finding.title}
                    description={asking ? DOCKER_REMEDIES[kind].advice : finding.detail}
                    trailing={
                      done ? (
                        // A pending change, in the hue every pending change takes (§3).
                        <span
                          className="hidden text-hint font-medium sm:inline"
                          style={{ color: "var(--git-modified)" }}
                        >
                          in the draft
                        </span>
                      ) : (
                        <Button size="xs" variant="outline" tabIndex={-1} aria-hidden>
                          <Wrench className="size-3" />
                          {DOCKER_REMEDIES[kind].label}
                        </Button>
                      )
                    }
                  >
                    {asking ? (
                      <div className="ml-9 flex min-w-0 flex-wrap items-end gap-2">
                        <label className="min-w-0 flex-1 basis-56 space-y-1">
                          <span className="text-hint text-muted-foreground">
                            {kind === "latest" ? "Version or digest" : "Readiness command"}
                          </span>
                          <Input
                            id={`remedy-${kind}`}
                            autoFocus
                            value={values[kind] ?? ""}
                            placeholder={
                              kind === "latest"
                                ? `${detail.image.split(":")[0]}:<version>`
                                : "curl -fsS http://localhost/health"
                            }
                            className="font-mono text-xs"
                            onChange={(event) =>
                              setValues((current) => ({ ...current, [kind]: event.target.value }))
                            }
                            onKeyDown={(event) => {
                              if (event.key === "Enter") prepare(kind)
                            }}
                          />
                        </label>
                        <Button size="sm" variant="outline" onClick={() => prepare(kind)}>
                          Prepare change
                        </Button>
                      </div>
                    ) : undefined}
                  </ChoiceRow>
                )
              })}
              {elsewhere.map((finding, index) => (
                <ChoiceRow
                  key={finding.id}
                  index={remedies.length + index}
                  disabled
                  verb={finding.title}
                  className={LEVEL_GROUND[levelOf(finding)]}
                  leading={<SeverityMark finding={finding} />}
                  title={finding.title}
                  description={finding.detail}
                  trailing={
                    finding.actionLabel && (
                      <span className="hidden text-hint text-muted-foreground sm:inline">
                        {finding.actionLabel} from the Overview
                      </span>
                    )
                  }
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      <CurrentSettings detail={detail} findings={findings} />

      {/* One frame around the draft: it is a working region, and its head is
          where the draft says what it changes. */}
      <section className="relative min-w-0">
        {replacing && (
          <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
            <BorderBeam size={120} duration={4} />
          </span>
        )}
        <Panel>
          <PanelHeader
            title={<label htmlFor="remedy-spec">Replacement specification</label>}
            actions={
              replacing ? (
                <TextShimmer className="text-hint font-medium">Replacing…</TextShimmer>
              ) : changed.length > 0 ? (
                <span className="flex min-w-0 flex-wrap items-center justify-end gap-x-2 gap-y-1">
                  <span className="text-hint text-muted-foreground">Edited</span>
                  {changed.map((field) => (
                    <Tag key={field} mono style={{ color: "var(--git-modified)" }}>
                      {field}
                    </Tag>
                  ))}
                </span>
              ) : (
                <span className="text-hint text-muted-foreground">As it runs now</span>
              )
            }
          />
          <PanelBody className="space-y-3">
            <p className="text-hint text-muted-foreground">
              Prepare a change from a card above or edit the specification here; nothing runs until
              you review and confirm. It contains credentials, so keep it private. It holds the
              settings the dashboard can carry across a replacement; compare Inspect for anything
              else the Engine keeps. The name is retained, and the replacement starts the service.
            </p>
            <Textarea
              id="remedy-spec"
              className="min-h-80 font-mono text-hint"
              value={content}
              onChange={(event) => edit(event.target.value)}
              spellCheck={false}
              aria-invalid={error ? true : undefined}
            />
            {error && (
              <p role="alert" className="text-hint text-destructive">
                {error}
              </p>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant={preview ? "outline" : "default"}
                pending={busy}
                disabled={!changed.length || busy}
                onClick={review}
              >
                Preview replacement
              </Button>
              {!changed.length && (
                <span className="text-hint text-muted-foreground">
                  Change a field to review a replacement.
                </span>
              )}
            </div>
          </PanelBody>
        </Panel>
      </section>

      {preview && (
        <Panel plain className="animate-rise">
          <PanelHeader title="Reviewed Compose equivalent" />
          <PanelBody className="space-y-3">
            <Well className="max-h-80 overflow-auto text-xs leading-relaxed break-all whitespace-pre-wrap">
              <YamlSnippet text={preview.rendered.compose} />
            </Well>
            <p className="text-hint text-muted-foreground">
              Docker validates the submitted settings when creating the replacement. Preview shows
              the settings; it cannot prove the application will work.
            </p>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                disabled={!can("destructive") || !!source.data.autoRemove || replacing}
                onClick={apply}
              >
                Replace with reviewed configuration
              </Button>
              {source.data.autoRemove && (
                <span className="text-hint text-muted-foreground">
                  This auto-remove container cannot be replaced in place. Create a replacement under
                  a new name so the original remains recoverable.
                </span>
              )}
              {!can("destructive") && (
                <span className="text-hint text-muted-foreground">
                  Destructive capability is required to apply.
                </span>
              )}
            </div>
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}

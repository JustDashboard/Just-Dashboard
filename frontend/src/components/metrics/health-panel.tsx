"use client"

import { useMemo, useState } from "react"
import type { Health, HealthArea, HealthFinding } from "@/lib/types"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { healthInvestigation } from "@/lib/server-advisor"
import { CheckCircle, RefreshClockwise, Wrench } from "@/components/icons"
import { HealthInvestigation } from "@/components/metrics/health-investigation"
import {
  AREA,
  AREA_ORDER,
  LEVEL_BAR,
  LEVEL_GROUND,
  LEVEL_TEXT,
  LEVEL_WORD,
  findingArea,
  findingGauge,
  heldFor,
  segmentState,
  type HealthStripArea,
} from "@/components/metrics/health-vocabulary"
import { Segment } from "@/components/deploy/run-pipeline"
import { useNow } from "@/components/deploy/vocabulary"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Disclosure } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Meter } from "@/components/meter"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { ErrorState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { type LinkedFinding, verdictWith, worstFirst } from "@/components/overview/attention"

/** A finding the reader fixed here stays on screen, green, for this long. */
const RESOLVED_FOR = 90_000

/**
 * What needs the operator, and what was checked to say so.
 *
 * Three layers, each answering a question the old flat list left open. The
 * strip is every area of the machine with its own verdict and current
 * reading, so a clean server reads as *checked* rather than as empty and a
 * partial one shows which area could not be read. The cards are only what is
 * worth acting on — critical and warning — each with the figure it was judged
 * on against the line it crossed, and each opening the evidence and the fix.
 * Notices fold under one line: true, and not urgent.
 */
export function HealthPanel({
  health,
  loading,
  plain,
  emptyLabel,
  className,
  onChanged,
  error,
  also,
}: {
  health: Health | undefined
  loading: boolean
  /**
   * What the other modules found, read into the same list rather than a
   * second one beside it: two lists answering "what needs me" is one too
   * many. Each opens the page that owns it.
   */
  also?: LinkedFinding[]
  /** Drawn as a titled list on the page rather than in a frame — see `Panel`. */
  plain?: boolean
  /** What "nothing found" covers, when the caller has folded more checks in. */
  emptyLabel?: string
  className?: string
  error?: Error
  onChanged?: () => void
}) {
  const [selected, setSelected] = useState<HealthFinding | null>(null)
  // The `checkedAt` a manual check was asked over: the strip sweeps until a
  // newer verdict lands, so the press visibly does something.
  const [checkingOver, setCheckingOver] = useState<string | null>(null)
  const [fixed, setFixed] = useState<Record<string, { title: string; at: number }>>({})
  const now = useNow(10_000)

  const refresh = () => {
    onChanged?.()
    window.dispatchEvent(new Event("jd:health-changed"))
  }
  const checkNow = () => {
    setCheckingOver(health?.checkedAt ?? "")
    refresh()
  }
  // A failed read ends the sweep as surely as a fresh verdict does.
  const checking = checkingOver !== null && checkingOver === (health?.checkedAt ?? "") && !error

  const findings = health?.findings ?? []
  const present = new Set(findings.map((finding) => finding.id))
  const resolved = Object.entries(fixed).filter(
    ([id, entry]) => !present.has(id) && now - entry.at < RESOLVED_FOR,
  )

  if (loading && !health) return <HealthSkeleton plain={plain} className={className} />
  if (!health) return error ? <HealthReadError error={error} onRetry={refresh} /> : null

  const linkedAll = also ?? []
  const status = verdictWith(health.status, linkedAll)
  // One ranking across the machine and the modules: a failed deploy outranks
  // a disk at 86%, whichever list it came from.
  const urgent = worstFirst<{
    level: HealthFinding["level"]
    host?: HealthFinding
    linked?: LinkedFinding
  }>([
    ...findings
      .filter((finding) => finding.level !== "notice")
      .map((finding) => ({ level: finding.level, host: finding })),
    ...linkedAll
      .filter((finding) => finding.level !== "notice")
      .map((finding) => ({ level: finding.level, linked: finding })),
  ])
  const notes = findings.filter((finding) => finding.level === "notice")
  const linkedNotes = linkedAll.filter((finding) => finding.level === "notice")

  return (
    <Panel plain={plain} className={className}>
      <PanelHeader
        title="Health"
        actions={
          <div className="flex items-center gap-3">
            <span className="numeric hidden text-hint text-muted-foreground sm:inline">
              {checking ? "Checking…" : `Checked ${relativeTime(health.checkedAt)}`}
            </span>
            <IconAction label="Check again now" onClick={checkNow} disabled={checking}>
              <RefreshClockwise className={cn(checking && "animate-spin")} />
            </IconAction>
            <HealthVerdict status={status} partial={!!health.silences?.length} />
          </div>
        }
      />
      <PanelBody className="space-y-4">
        {error && <HealthReadError error={error} onRetry={refresh} />}

        <AreaStrip
          health={health}
          also={also}
          checking={checking}
          onOpen={(finding) => setSelected(finding)}
        />

        {resolved.length > 0 && (
          <ul className="space-y-1.5" aria-label="Resolved here">
            {resolved.map(([id, entry]) => (
              <li
                key={id}
                className="flex min-w-0 animate-rise items-center gap-2.5 rounded-xl border border-rule-success bg-wash-success px-3 py-2.5 text-body"
              >
                <CheckCircle className="size-4 shrink-0 text-success" />
                <span className="min-w-0 truncate font-medium">{entry.title}</span>
                <span className="ml-auto shrink-0 text-hint text-success">Resolved</span>
              </li>
            ))}
          </ul>
        )}

        {urgent.length > 0 ? (
          <ChoiceList aria-label="Needs attention">
            {urgent.map(({ host, linked }, index) =>
              host ? (
                <FindingCard
                  key={host.id}
                  finding={host}
                  index={index}
                  now={now}
                  onOpen={() => setSelected(host)}
                />
              ) : (
                linked && <LinkedCard key={linked.id} finding={linked} index={index} />
              ),
            )}
          </ChoiceList>
        ) : (
          resolved.length === 0 && (
            <Notice tone="success" icon={CheckCircle} title="Nothing needs you">
              {health.silences?.length
                ? "Every check that could run is within its limits."
                : (emptyLabel ??
                  (health.recorded
                    ? "Capacity, memory, CPU, pressure, network, services and containers are all within limits."
                    : "Every check passed on the current reading."))}
            </Notice>
          )
        )}

        {notes.length + linkedNotes.length > 0 && (
          <Disclosure
            quiet
            summary={plural(notes.length + linkedNotes.length, "note")}
            facts={[...notes, ...linkedNotes].map((note) => note.title).join(" · ")}
          >
            <RowList className="pt-1">
              {notes.map((note) => (
                <Row
                  key={note.id}
                  title={note.title}
                  subtitle={note.detail}
                  onClick={healthInvestigation(note.id) ? () => setSelected(note) : undefined}
                />
              ))}
              {linkedNotes.map((note) => (
                <Row
                  key={note.id}
                  title={note.title}
                  subtitle={note.detail}
                  href={note.action?.href}
                />
              ))}
            </RowList>
          </Disclosure>
        )}

        {!!health.silences?.length && (
          <p className="text-hint text-muted-foreground">
            <span className="font-medium">Not assessed: </span>
            {health.silences.join(" · ")}
          </p>
        )}
      </PanelBody>
      <HealthInvestigation
        finding={selected}
        onOpenChange={(open) => !open && setSelected(null)}
        onChanged={refresh}
        onFixed={(finding) =>
          setFixed((current) => ({
            ...current,
            [finding.id]: { title: finding.title, at: Date.now() },
          }))
        }
      />
    </Panel>
  )
}

/**
 * Every area the verdict covers, as the release path draws a run: one segment
 * each, green where it passed, amber and red where it did not, dashed where
 * it could not be read, and sweeping while a check is in flight.
 */
function AreaStrip({
  health,
  also,
  checking,
  onOpen,
}: {
  health: Health
  also?: LinkedFinding[]
  checking: boolean
  onOpen: (finding: HealthFinding) => void
}) {
  const cells = useMemo(() => {
    const byArea = new Map<HealthArea, HealthFinding[]>()
    for (const finding of health.findings) {
      const area = findingArea(finding)
      byArea.set(area, [...(byArea.get(area) ?? []), finding])
    }
    const verdicts = new Map(health.areas?.map((area) => [area.id, area]))
    const rows: {
      id: HealthStripArea
      status: Health["status"] | "unknown"
      summary: string
      worst?: HealthFinding
    }[] = AREA_ORDER.filter((id) => verdicts.has(id) || byArea.has(id)).map((id) => {
      const found = worstFirst(byArea.get(id) ?? [])
      const verdict = verdicts.get(id)
      return {
        id,
        status: verdict?.status ?? found[0]?.level ?? "ok",
        summary: verdict?.summary ?? (found.length ? plural(found.length, "finding") : "ok"),
        worst: found[0],
      }
    })
    // Only where the caller folded the other modules in: on Metrics there is
    // nothing behind the cell, and "all clear" would be a claim about nothing.
    if (also) {
      const worst = worstFirst(also)[0]
      rows.push({
        id: "modules",
        status: worst ? verdictWith("ok", also) : "ok",
        summary: worst ? `${also.length} to review` : "all clear",
      })
    }
    return rows
  }, [health, also])

  if (cells.length === 0) return null
  return (
    <ol
      aria-label="Areas checked"
      className="-mx-1.5 grid grid-cols-2 gap-x-1 gap-y-2 sm:grid-cols-4 xl:[grid-template-columns:repeat(var(--areas),minmax(0,1fr))]"
      style={{ "--areas": cells.length } as React.CSSProperties}
    >
      {cells.map((cell, index) => {
        const { label, icon: Icon } = AREA[cell.id]
        const bad = cell.status === "warning" || cell.status === "critical"
        const body = (
          <>
            <Segment state={segmentState(cell.status, checking)} />
            <span className="mt-2 flex min-w-0 items-center gap-1.5">
              <Icon className="size-3 shrink-0 text-brand" />
              <span className="eyebrow truncate">{label}</span>
            </span>
            <span
              title={cell.summary}
              className={cn(
                "numeric mt-0.5 block truncate text-body",
                cell.status === "critical" && "text-destructive",
                cell.status === "warning" && "text-warning",
                cell.status === "unknown" && "text-muted-foreground",
              )}
            >
              {cell.status === "unknown" && !cell.summary ? "not readable" : cell.summary}
            </span>
          </>
        )
        return (
          <li
            key={cell.id}
            className="min-w-0 animate-rise"
            style={{ animationDelay: `${index * 30}ms` }}
          >
            {bad && cell.worst ? (
              <button
                type="button"
                onClick={() => onOpen(cell.worst!)}
                aria-label={`${label}: ${cell.summary}. Open what was found`}
                className="block w-full min-w-0 rounded-md px-1.5 pt-1.5 pb-1 text-left focus-ring transition-colors hover:bg-row-hover"
              >
                {body}
              </button>
            ) : (
              <div className="px-1.5 pt-1.5 pb-1">{body}</div>
            )}
          </li>
        )
      })}
    </ol>
  )
}

/** A finding worth acting on: the figure it was judged on, how long, and the way to the fix. */
function FindingCard({
  finding,
  index,
  now,
  onOpen,
}: {
  finding: HealthFinding
  index: number
  now: number
  onOpen: () => void
}) {
  const { icon: Icon } = AREA[findingArea(finding)]
  const gauge = findingGauge(finding)
  const held = heldFor(finding.since, now)
  const target = healthInvestigation(finding.id)
  const fixable = target && target.kind !== "external"
  return (
    <ChoiceRow
      index={index}
      verb={`${fixable ? "Fix" : "Review"}: ${finding.title}`}
      onSelect={target ? onOpen : undefined}
      disabled={!target}
      className={LEVEL_GROUND[finding.level]}
      leading={<SeverityMark level={finding.level} icon={Icon} />}
      title={finding.title}
      description={finding.detail}
      trailing={
        <span className="flex items-center gap-3">
          {gauge && (
            <span className="hidden w-28 shrink-0 space-y-1 md:block">
              <span className={cn("numeric block text-right text-hint", LEVEL_TEXT[finding.level])}>
                {gauge.label}
              </span>
              <Meter
                value={gauge.value}
                mark={gauge.mark}
                tone={finding.level === "critical" ? "danger" : "warning"}
                size="thin"
                label={finding.title}
              />
            </span>
          )}
          {held && (
            <span className="numeric hidden text-hint text-muted-foreground sm:inline">
              for {held}
            </span>
          )}
          {target && (
            <Button size="xs" variant="outline" onClick={onOpen} tabIndex={-1} aria-hidden>
              {fixable && <Wrench className="size-3" />}
              {fixable ? "Fix" : "Review"}
            </Button>
          )}
        </span>
      }
    />
  )
}

/** The level as a bar of its hue beside the area's glyph in the same hue. */
function SeverityMark({
  level,
  icon: Icon,
}: {
  level: HealthFinding["level"]
  icon: React.ComponentType<{ className?: string }>
}) {
  return (
    <span className="flex items-center gap-2.5">
      <span aria-hidden className={cn("h-7 w-0.5 rounded-full", LEVEL_BAR[level])} />
      <Icon className={cn("size-4", LEVEL_TEXT[level])} />
    </span>
  )
}

/** Another page's finding: the same card, opening the page that owns the remedy. */
function LinkedCard({ finding, index }: { finding: LinkedFinding; index: number }) {
  const { icon: Icon } = AREA.modules
  return (
    <ChoiceRow
      index={index}
      verb={finding.action ? `${finding.action.label}: ${finding.title}` : finding.title}
      href={finding.action?.href}
      disabled={!finding.action}
      className={LEVEL_GROUND[finding.level]}
      leading={<SeverityMark level={finding.level} icon={Icon} />}
      title={finding.title}
      description={finding.detail}
      trailing={
        <span className="flex items-center gap-3">
          {finding.meta && (
            <span className="hidden text-hint text-muted-foreground sm:inline">{finding.meta}</span>
          )}
          {finding.action && (
            <span className="hidden text-hint font-medium sm:inline">{finding.action.label}</span>
          )}
        </span>
      }
    />
  )
}

function HealthSkeleton({ plain, className }: { plain?: boolean; className?: string }) {
  return (
    <Panel plain={plain} className={className}>
      <PanelHeader title="Health" />
      <PanelBody className="space-y-4">
        <ol className="-mx-1.5 grid grid-cols-2 gap-x-1 gap-y-2 sm:grid-cols-4 xl:grid-cols-7">
          {AREA_ORDER.map((area) => (
            <li key={area} className="space-y-2 px-1.5 pt-1.5">
              <Segment state="running" />
              <Skeleton className="h-3 w-16" />
              <Skeleton className="h-4 w-20" />
            </li>
          ))}
        </ol>
        <Skeleton className="h-14 w-full rounded-xl" />
      </PanelBody>
    </Panel>
  )
}

/** The one-word verdict, small enough for the top bar and a panel header alike. */
export function HealthVerdict({
  status,
  className,
  partial = false,
}: {
  status: Health["status"]
  partial?: boolean
  className?: string
}) {
  return (
    <Status
      verdict={status}
      label={status === "ok" && partial ? "Partial assessment" : LEVEL_WORD[status]}
      className={className}
    />
  )
}

function HealthReadError({ error, onRetry }: { error: Error; onRetry: () => void }) {
  return (
    <div className="space-y-2">
      <ErrorState error={error} />
      <Button size="xs" variant="outline" onClick={onRetry}>
        Try again
      </Button>
    </div>
  )
}

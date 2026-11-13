"use client"

import { ArrowRight, Cross } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { EmptyNote } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/ui/button"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import {
  activeCheck,
  stageReading,
  type ExternalCheck,
  type ExternalVantage,
} from "@/lib/network-external"
import {
  checkOutcome,
  checkTarget,
  checkTime,
  familyWord,
} from "@/components/network/external/reading"
import { calendarTime } from "@/components/network/external/time"

const STAGES = ["dns", "tcp", "tls"] as const
const STATUS_WORD: Record<ExternalCheck["status"], string> = {
  queued: "Queued",
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
  expired: "Expired",
}

/**
 * Every check kept, newest first, one line each: which source, which
 * service, how the check stands and what each stage measured. A stage the
 * agent did not report reads "Unknown" — never "Failed" — because a check
 * that expired or was cancelled measured nothing.
 */
export function ExternalChecks({
  checks,
  vantages,
  loading,
  selected,
  onSelect,
  canAct,
  busy,
  onCancel,
  onCompare,
  onCompareFrom,
}: {
  checks: ExternalCheck[]
  vantages: ExternalVantage[]
  loading: boolean
  selected?: ExternalCheck
  onSelect: (id: string) => void
  canAct: boolean
  busy: boolean
  onCancel: (check: ExternalCheck) => void
  onCompare?: () => void
  onCompareFrom: (check: ExternalCheck) => void
}) {
  const sourceOf = (check: ExternalCheck) => vantages.find((v) => v.id === check.vantageId)
  const retained = checks.filter((check) => check.result).length
  return (
    <Panel aria-label="Checks">
      <PanelHeader
        title={
          <>
            Checks
            {checks.length > 0 && (
              <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                {checks.length}
              </span>
            )}
          </>
        }
        actions={
          onCompare && (
            <Button size="sm" variant="outline" disabled={retained < 2} onClick={onCompare}>
              Compare
            </Button>
          )
        }
      />
      {loading ? (
        <div aria-hidden className="divide-y divide-hairline">
          {[0, 1, 2].map((i) => (
            <div key={i} className="flex h-12 items-center gap-6 px-4">
              <Skeleton className="h-3.5 w-36" />
              <Skeleton className="h-3.5 w-28" />
              <Skeleton className="ml-auto h-3.5 w-20" />
            </div>
          ))}
        </div>
      ) : checks.length === 0 ? (
        <EmptyNote className="px-5 py-6">
          No checks kept. Each check is kept for seven days.
        </EmptyNote>
      ) : (
        <>
          {/* Capped, so the chosen check's reading under it stays near the row
              that was pressed however many checks seven days hold. */}
          <Table className="table-fixed" containerClassName="max-h-[21.5rem] max-md:hidden">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-[19%]">Source</TableHead>
                <TableHead>Service</TableHead>
                <TableHead className="w-32">State</TableHead>
                <TableHead className="w-[5.25rem]">DNS</TableHead>
                <TableHead className="w-[5.25rem]">TCP</TableHead>
                <TableHead className="w-[5.25rem]">TLS</TableHead>
                <TableHead className="w-24">When</TableHead>
                <TableHead className="w-14">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {checks.map((check) => {
                const source = sourceOf(check)
                const name = source?.name ?? check.vantageId
                return (
                  <TableRow
                    key={check.id}
                    data-state={selected?.id === check.id ? "selected" : undefined}
                    onActivate={() => onSelect(check.id)}
                    className="h-12"
                  >
                    <TableCell className="truncate">
                      <button
                        type="button"
                        aria-pressed={selected?.id === check.id}
                        onClick={() => onSelect(check.id)}
                        className="truncate rounded-sm text-body font-medium focus-ring hover:text-primary"
                      >
                        {name}
                      </button>
                    </TableCell>
                    <TableCell className="truncate font-mono text-hint">
                      {checkTarget(check, vantages) ?? "Unknown target"}{" "}
                      <span className="text-muted-foreground">
                        {familyWord(check.request.family)} · {check.request.port}
                      </span>
                    </TableCell>
                    <TableCell>
                      <CheckState check={check} />
                    </TableCell>
                    {STAGES.map((stage) => (
                      <TableCell key={stage} className="truncate">
                        <StageWord check={check} stage={stage} />
                      </TableCell>
                    ))}
                    <TableCell className="truncate text-muted-foreground">
                      <time dateTime={checkTime(check)} title={calendarTime(checkTime(check))}>
                        {relativeTime(checkTime(check))}
                      </time>
                    </TableCell>
                    <TableCell className="text-right">
                      <CheckVerbs
                        check={check}
                        name={name}
                        canAct={canAct}
                        busy={busy}
                        onCancel={onCancel}
                        onCompareFrom={onCompareFrom}
                      />
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
          {/* A phone keeps every check as two lines of one height: who and how
              it stands, then what each stage measured. */}
          <ul aria-label="Checks" className="divide-y divide-hairline md:hidden">
            {checks.map((check) => {
              const name = sourceOf(check)?.name ?? check.vantageId
              return (
                <li
                  key={check.id}
                  data-state={selected?.id === check.id ? "selected" : undefined}
                  className="flex h-16 items-center gap-3 px-4 data-[state=selected]:bg-accent"
                >
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex min-w-0 items-center gap-2">
                      <button
                        type="button"
                        aria-pressed={selected?.id === check.id}
                        onClick={() => onSelect(check.id)}
                        className="min-w-0 truncate rounded-sm text-body font-medium focus-ring"
                      >
                        {name}
                      </button>
                      <CheckState check={check} className="ml-auto shrink-0" />
                    </div>
                    <p className="flex min-w-0 gap-3 truncate text-hint">
                      <span className="text-muted-foreground">
                        {familyWord(check.request.family)} · {check.request.port}
                      </span>
                      {STAGES.map((stage) => (
                        <span key={stage} className="shrink-0">
                          <span className="text-muted-foreground">{stage.toUpperCase()} </span>
                          <StageWord check={check} stage={stage} />
                        </span>
                      ))}
                    </p>
                  </div>
                  <CheckVerbs
                    check={check}
                    name={name}
                    canAct={canAct}
                    busy={busy}
                    onCancel={onCancel}
                    onCompareFrom={onCompareFrom}
                  />
                </li>
              )
            })}
          </ul>
        </>
      )}
    </Panel>
  )
}

/**
 * How a check stands, in one word: what it measured where it measured
 * anything, and where it is in its queue where it did not.
 */
function CheckState({ check, className }: { check: ExternalCheck; className?: string }) {
  const outcome = checkOutcome(check)
  const measured = outcome.kind !== "none" && outcome.kind !== "pending"
  const word = measured ? outcome.word : STATUS_WORD[check.status]
  return (
    <span
      title={
        check.result ? `${STATUS_WORD[check.status]} · Agent reported` : "No accepted measurement"
      }
      className={className}
    >
      <Status tone={outcome.tone} label={word[0].toUpperCase() + word.slice(1)} />
    </span>
  )
}

/** A stage's word, green or red only where the agent measured it. */
function StageWord({ check, stage }: { check: ExternalCheck; stage: (typeof STAGES)[number] }) {
  const reading = stageReading(check, stage)
  return (
    <span title={reading.detail} className={cn("text-hint", stageTone(check, stage))}>
      {reading.label}
    </span>
  )
}

function stageTone(check: ExternalCheck, name: (typeof STAGES)[number]) {
  const stage = check.result?.stages.find((item) => item.name === name)
  if (stageReading(check, name).good) return "text-success"
  if (stage?.basis === "measured" && ["failed", "refused", "unavailable"].includes(stage.state))
    return "text-destructive"
  return "text-muted-foreground"
}

function CheckVerbs({
  check,
  name,
  canAct,
  busy,
  onCancel,
  onCompareFrom,
}: {
  check: ExternalCheck
  name: string
  canAct: boolean
  busy: boolean
  onCancel: (check: ExternalCheck) => void
  onCompareFrom: (check: ExternalCheck) => void
}) {
  if (!canAct) return null
  const verbs: Verb[] = []
  if (activeCheck(check))
    verbs.push({
      key: "cancel",
      label: "Cancel check",
      icon: Cross,
      inline: true,
      disabled: busy,
      run: () => onCancel(check),
    })
  if (check.result)
    verbs.push({
      key: "compare",
      label: "Compare with…",
      icon: ArrowRight,
      run: () => onCompareFrom(check),
    })
  if (verbs.length === 0) return null
  return (
    <VerbActions
      dim
      verbs={verbs}
      menuLabel={`Actions for ${name}'s check`}
      className="justify-end"
    />
  )
}

/**
 * The chosen check in full: who asked and when, the path the agent took, and
 * each stage's basis, time and words. A check with no accepted measurement
 * says so rather than reading as a failure.
 */
export function CheckReading({
  check,
  vantage,
  target,
}: {
  check: ExternalCheck
  vantage?: ExternalVantage
  target?: string
}) {
  const result = check.result
  return (
    <Panel plain aria-label="Selected check" id="external-check" className="scroll-mt-6">
      <PanelHeader
        title={
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate">{vantage?.name ?? check.vantageId}</span>
            <ArrowRight aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate font-mono">{target ?? "Unknown target"}</span>
          </span>
        }
        actions={<CheckState check={check} />}
      />
      <PanelBody className="space-y-4">
        <p className="flex flex-wrap gap-x-3 gap-y-1 text-hint text-muted-foreground">
          <span>
            {familyWord(check.request.family)} · TCP {check.request.port}
            {check.request.tls ? " · TLS verified" : ""}
          </span>
          <span>
            {STATUS_WORD[check.status]} · {result ? "agent reported" : "no accepted measurement"}
          </span>
          <span>Requested by {check.startedBy}</span>
          <span>Deadline {calendarTime(check.expiresAt)}</span>
          <span>Received {calendarTime(check.completedAt)}</span>
        </p>
        {result ? (
          <>
            <p className="font-mono text-body break-all">
              {result.sourceAddress || "Source unknown"} →{" "}
              {result.address || "No connected destination"}:{check.request.port}
            </p>
            <dl className="divide-y divide-hairline border-y border-hairline">
              {result.stages.map((stage) => {
                const reading = stageReading(check, stage.name)
                return (
                  <div
                    key={stage.name}
                    className="grid min-w-0 grid-cols-[3rem_minmax(0,8rem)_minmax(0,1fr)] items-center gap-3 py-2 text-body sm:grid-cols-[3rem_9rem_7rem_minmax(0,1fr)]"
                  >
                    <dt className="font-mono text-hint text-muted-foreground">
                      {stage.name.toUpperCase()}
                    </dt>
                    <dd className={cn("truncate", stageTone(check, stage.name), "text-body")}>
                      {reading.label}
                    </dd>
                    <dd className="text-hint text-muted-foreground max-sm:hidden">
                      {stage.basis} · {stage.durationMs} ms
                    </dd>
                    <dd className="truncate text-hint text-muted-foreground" title={stage.detail}>
                      {stage.detail}
                    </dd>
                  </div>
                )
              })}
            </dl>
            {result.certificate && (
              <p className="font-mono text-hint break-all text-muted-foreground">
                TLS certificate SHA256: {result.certificate.sha256}
              </p>
            )}
            {result.limitations.map((limit, i) => (
              <p key={i} className="text-hint text-muted-foreground">
                {limit}
              </p>
            ))}
          </>
        ) : (
          <p className="text-body text-muted-foreground">
            No accepted measurement. Queue, expiry or cancellation states do not establish service
            reachability.
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

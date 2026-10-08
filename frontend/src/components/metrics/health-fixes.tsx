"use client"

import Link from "next/link"
import { useLayoutEffect, useRef, useState } from "react"
import { get, post } from "@/lib/api"
import { clock, plural, relativeTime } from "@/lib/format"
import { dockerSource, journalSource } from "@/lib/log-sources"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type {
  ContainerDetail,
  HealthSubject,
  LogLine,
  LogSearchResult,
  SystemdUnit,
  SystemdUnitDetail,
} from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Box, CheckCircle, Servers, Warning } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { Well } from "@/components/panel"
import { ProductLogo, containerProduct, unitProduct } from "@/components/product-logo"
import { UnitJournalSheet } from "@/components/procs/unit-journal"
import { useUnitControl } from "@/components/procs/unit-actions"
import { ago, failureWords, unitStateWord, unitTone } from "@/components/procs/units"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { BorderBeam } from "@/components/ui/border-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"

/** Enough of a log to read why something stopped, without a second sheet. */
const LOG_LINES = 14
const SEVERE = new Set(["critical", "error", "fatal", "emerg", "alert"])

/**
 * Each failed service, opened where it was found: why it stopped, the last
 * lines it wrote, and the three honest answers — restart it, clear a failure
 * that no longer matters, or stop it coming back at boot — each followed by a
 * fresh read of the unit so the outcome is seen rather than assumed.
 */
export function ServiceFixes({
  subjects = [],
  onChanged,
}: {
  subjects?: HealthSubject[]
  onChanged: () => void
}) {
  const units = subjects.filter((subject) => subject.kind === "unit")
  return (
    <section aria-label="Failed services" className="space-y-3">
      <h3 className="text-title font-semibold">
        {units.length === 1 ? "Fix the service" : `Fix ${plural(units.length, "service")}`}
      </h3>
      <p className="text-body leading-relaxed text-muted-foreground">
        Restart it once the lines below say why it stopped. Clear the failure when it is old and
        nothing depends on it; disable it when this server no longer needs it.
      </p>
      {units.map((subject) => (
        <FailedUnit key={subject.id} name={subject.id} onChanged={onChanged} />
      ))}
    </section>
  )
}

function FailedUnit({ name, onChanged }: { name: string; onChanged: () => void }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [reads, setReads] = useState(0)
  const [acted, setActed] = useState<string | null>(null)
  const [sheet, setSheet] = useState(false)
  const now = useNow(30_000)
  // Polled while open: a restart passes through activating, and the outcome
  // line should land when the unit settles rather than when the request did.
  const detail = usePoll(
    (signal) => get<SystemdUnitDetail>(`/systemd/${encodeURIComponent(name)}`, undefined, signal),
    3000,
    [name],
  )
  const journal = usePoll(
    async (signal) =>
      (
        await get<LogSearchResult>(
          "/logs/search",
          { source: journalSource(name), limit: LOG_LINES },
          signal,
        )
      ).lines,
    0,
    [name, reads],
  )
  const { pending, act } = useUnitControl(() => {
    setReads((n) => n + 1)
    detail.refresh()
    onChanged()
  })
  const unit = detail.data?.unit
  const busy = pending[name]
  const run = (action: string, progressive: string, phrase?: string) => {
    if (!unit) return
    setActed(action)
    void act(unit, action, progressive, phrase).catch(() => setActed(null))
  }
  const restart = () =>
    unit &&
    confirm({
      title: "Restart service",
      confirmLabel: "Restart",
      subject: {
        mark: <ProductLogo id={unitProduct(name)} size="sm" fallback={Servers} />,
        name,
        facts: unit.description,
      },
      description: (
        <p>
          systemd starts it again from its unit file. If the cause is still there it will fail
          again, and the lines it writes appear here.
        </p>
      ),
      action: async (phrase) => {
        setActed("restart")
        try {
          // systemd refuses to start a unit that hit its start limit until the
          // failure is cleared, so a bare restart there is a button that fails.
          if (unit.result === "start-limit-hit") await act(unit, "reset-failed", "Clearing")
          await act(unit, "restart", "Restarting", phrase)
        } catch (err) {
          setActed(null)
          throw err
        }
      },
    })
  const disable = () =>
    unit &&
    confirm({
      title: "Disable at boot",
      confirmLabel: "Disable",
      subject: {
        mark: <ProductLogo id={unitProduct(name)} size="sm" fallback={Servers} />,
        name,
        facts: unit.description,
      },
      description: (
        <p>
          It stops being started at boot and its failure is cleared. Its unit file stays, so it can
          be enabled again from Services.
        </p>
      ),
      action: async () => {
        setActed("disable")
        await act(unit, "disable", "Disabling")
        await act(unit, "reset-failed", "Clearing").catch(() => undefined)
      },
    })

  return (
    <article className="relative space-y-3 rounded-xl border bg-card p-4">
      {busy && (
        <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
          <BorderBeam size={80} duration={4} />
        </span>
      )}
      <header className="flex min-w-0 items-center gap-3">
        <ProductLogo id={unitProduct(name)} size="sm" fallback={Servers} />
        <div className="min-w-0 flex-1">
          <p className="truncate text-body font-medium">{name}</p>
          {unit?.description && (
            <p className="truncate text-hint text-muted-foreground">{unit.description}</p>
          )}
        </div>
        {busy ? (
          <TextShimmer className="text-xs font-medium">{`${busy}…`}</TextShimmer>
        ) : (
          unit && <Status tone={unitTone(unit)} label={unitStateWord(unit)} />
        )}
      </header>

      {detail.error && !unit && <ErrorState error={detail.error} />}
      {unit && <UnitReason unit={unit} now={now} />}
      {unit && acted && !busy && <UnitOutcome unit={unit} action={acted} />}

      <LogTail lines={journal.data} loading={journal.loading} error={journal.error} />

      <div className="flex flex-wrap items-center gap-2">
        {can("destructive") ? (
          <Button size="sm" onClick={restart} disabled={!unit || !!busy}>
            Restart
          </Button>
        ) : (
          can("service.control") && (
            <Button
              size="sm"
              onClick={() =>
                unit?.result === "start-limit-hit"
                  ? void act(unit, "reset-failed", "Clearing")
                      .then(() => run("start", "Starting"))
                      .catch(() => undefined)
                  : run("start", "Starting")
              }
              disabled={!unit || !!busy || unit.activeState === "active"}
            >
              Start
            </Button>
          )
        )}
        {can("service.control") && unit?.activeState === "failed" && (
          <Button
            size="sm"
            variant="outline"
            onClick={() => run("reset-failed", "Clearing")}
            disabled={!!busy}
          >
            Clear failure
          </Button>
        )}
        {can("system.admin") && unit?.enabled && (
          <Button size="sm" variant="outline" onClick={disable} disabled={!!busy}>
            Disable at boot
          </Button>
        )}
        <Button size="sm" variant="ghost" onClick={() => setSheet(true)}>
          Open service
        </Button>
      </div>
      <UnitJournalSheet
        unit={sheet ? name : null}
        initialTab="journal"
        onOpenChange={(open) => !open && setSheet(false)}
        onChanged={() => {
          detail.refresh()
          onChanged()
        }}
      />
      {dialog}
    </article>
  )
}

/** Why it stopped, in systemd's terms, and when. */
function UnitReason({ unit, now }: { unit: SystemdUnit; now: number }) {
  if (unit.activeState !== "failed") return null
  return (
    <p className="flex min-w-0 items-center gap-2 text-body">
      <Warning className="size-4 shrink-0 text-destructive" />
      <span className="min-w-0">
        <span className="font-medium text-destructive">{failureWords(unit)}</span>
        {unit.changedAt ? (
          <span className="text-muted-foreground"> · {ago(unit.changedAt, now)}</span>
        ) : null}
        {unit.restarts ? (
          <span className="text-muted-foreground">
            {" "}
            · {plural(unit.restarts, "automatic restart")}
          </span>
        ) : null}
      </span>
    </p>
  )
}

/** What the last control did, read from the unit as it is now. */
function UnitOutcome({ unit, action }: { unit: SystemdUnit; action: string }) {
  if (unit.activeState === "activating")
    return <TextShimmer className="text-body">Starting — waiting for it to settle…</TextShimmer>
  if (unit.activeState === "active")
    return <Outcome tone="success">Running again — the failure is gone.</Outcome>
  if (unit.activeState === "failed")
    return (
      <Outcome tone="danger">
        Failed again ({failureWords(unit)}). The lines below are from this attempt.
      </Outcome>
    )
  if (action === "disable") return <Outcome tone="success">Disabled at boot and cleared.</Outcome>
  return <Outcome tone="success">Failure cleared. It stays stopped until started.</Outcome>
}

function Outcome({ tone, children }: { tone: "success" | "danger"; children: React.ReactNode }) {
  const Icon = tone === "success" ? CheckCircle : Warning
  return (
    <p
      role="status"
      className={cn(
        "flex animate-rise items-center gap-2 rounded-lg border px-3 py-2 text-body",
        tone === "success"
          ? "border-rule-success bg-wash-success"
          : "border-rule-danger bg-wash-danger",
      )}
    >
      <Icon
        className={cn("size-4 shrink-0", tone === "success" ? "text-success" : "text-destructive")}
      />
      {children}
    </p>
  )
}

function LogTail({
  lines,
  loading,
  error,
}: {
  lines: LogLine[] | undefined
  loading: boolean
  error: Error | undefined
}) {
  const well = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    if (well.current) well.current.scrollTop = well.current.scrollHeight
  }, [lines])
  if (loading) return <LoadingRows rows={3} />
  if (error) return <ErrorState error={error} />
  if (!lines?.length) return <EmptyNote>It wrote nothing recently.</EmptyNote>
  return (
    <Well ref={well} className="max-h-60 space-y-0.5 text-foreground" aria-label="Recent log lines">
      {lines.map((line, index) => (
        <p
          key={`${line.timestamp}:${index}`}
          className={cn(
            "break-words whitespace-pre-wrap",
            SEVERE.has(line.level ?? "") && "text-destructive",
          )}
        >
          {line.timestamp && (
            <span className="text-muted-foreground">{clock(line.timestamp)} </span>
          )}
          {line.text}
        </p>
      ))}
    </Well>
  )
}

/**
 * Each container the runtime check named: its state and why, the last lines
 * it wrote, and the control that state calls for — a dead or stopped one is
 * started, a paused one resumed, an unhealthy or looping one restarted.
 */
export function ContainerFixes({
  subjects = [],
  onChanged,
}: {
  subjects?: HealthSubject[]
  onChanged: () => void
}) {
  const containers = subjects.filter((subject) => subject.kind === "container")
  return (
    <section aria-label="Affected containers" className="space-y-3">
      <h3 className="text-title font-semibold">
        {containers.length === 1
          ? "Fix the container"
          : `Fix ${plural(containers.length, "container")}`}
      </h3>
      {containers.map((subject) => (
        <ContainerFix key={subject.id} subject={subject} onChanged={onChanged} />
      ))}
    </section>
  )
}

function ContainerFix({ subject, onChanged }: { subject: HealthSubject; onChanged: () => void }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState<string | null>(null)
  const [acted, setActed] = useState(false)
  const [reads, setReads] = useState(0)
  const detail = usePoll(
    (signal) =>
      get<ContainerDetail>(
        `/docker/containers/${encodeURIComponent(subject.id)}`,
        undefined,
        signal,
      ),
    3000,
    [subject.id],
  )
  const logs = usePoll(
    async (signal) =>
      (
        await get<LogSearchResult>(
          "/logs/search",
          { source: dockerSource(subject.id), limit: LOG_LINES },
          signal,
        )
      ).lines,
    0,
    [subject.id, reads],
  )
  const container = detail.data
  const mark = (
    <ProductLogo
      id={container ? containerProduct(container) : undefined}
      size="sm"
      fallback={Box}
    />
  )
  const control = async (action: "start" | "restart" | "unpause", progressive: string) => {
    setBusy(progressive)
    try {
      await post(`/docker/containers/${encodeURIComponent(subject.id)}/${action}`)
      notify.success(`${subject.name}: ${action === "unpause" ? "resumed" : `${action}ed`}`)
      setActed(true)
      detail.refresh()
      setReads((n) => n + 1)
      onChanged()
    } catch (err) {
      notify.error(`Could not ${action} ${subject.name}`, err)
      throw err
    } finally {
      setBusy(null)
    }
  }
  const restart = () =>
    confirm({
      title: "Restart container",
      confirmLabel: "Restart",
      subject: { mark, name: subject.name, facts: container?.image },
      description: (
        <p>
          It stops and starts again with the same configuration. Requests to it fail until it is
          back up.
        </p>
      ),
      action: () => control("restart", "Restarting"),
    })

  const state = container?.state ?? subject.detail
  const healthy = container?.state === "running" && container.health !== "unhealthy"
  return (
    <article className="relative space-y-3 rounded-xl border bg-card p-4">
      {busy && (
        <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
          <BorderBeam size={80} duration={4} />
        </span>
      )}
      <header className="flex min-w-0 items-center gap-3">
        {mark}
        <div className="min-w-0 flex-1">
          <Link
            href={`/docker/containers/${encodeURIComponent(subject.id)}`}
            className="block truncate text-body font-medium focus-ring hover:underline"
          >
            {subject.name}
          </Link>
          {container && (
            <p className="truncate font-mono text-hint text-muted-foreground">{container.image}</p>
          )}
        </div>
        {busy ? (
          <TextShimmer className="text-xs font-medium">{`${busy}…`}</TextShimmer>
        ) : (
          <Status
            tone={healthy ? "running" : state === "paused" ? "warning" : "danger"}
            label={container?.health === "unhealthy" ? "unhealthy" : (state ?? "unknown")}
          />
        )}
      </header>
      {detail.error && !container && <ErrorState error={detail.error} />}
      {container && !healthy && (
        <p className="flex min-w-0 items-center gap-2 text-body">
          <Warning className="size-4 shrink-0 text-destructive" />
          <span className="min-w-0">
            {container.error ||
              (container.state === "restarting"
                ? `Crash-looping — restarted ${plural(container.restartCount, "time")}`
                : container.health === "unhealthy"
                  ? "Its health check is failing"
                  : `Exited with code ${container.exitCode}`)}
            {container.startedAt && (
              <span className="text-muted-foreground">
                {" "}
                · started {relativeTime(container.startedAt)}
              </span>
            )}
          </span>
        </p>
      )}
      {acted &&
        container &&
        !busy &&
        (healthy ? (
          <Outcome tone="success">Running{container.hasHealthcheck ? " and healthy" : ""}.</Outcome>
        ) : (
          <Outcome tone="danger">Still {state} — read the lines below for why.</Outcome>
        ))}
      <LogTail lines={logs.data} loading={logs.loading} error={logs.error} />
      <div className="flex flex-wrap items-center gap-2">
        {state === "paused"
          ? can("service.control") && (
              <Button size="sm" onClick={() => void control("unpause", "Resuming").catch(() => {})}>
                Resume
              </Button>
            )
          : state === "exited" || state === "dead" || state === "created"
            ? can("service.control") && (
                <Button size="sm" onClick={() => void control("start", "Starting").catch(() => {})}>
                  Start
                </Button>
              )
            : can("destructive") && (
                <Button size="sm" onClick={restart} disabled={!!busy}>
                  Restart
                </Button>
              )}
        <Button size="sm" variant="ghost" asChild>
          <Link href={`/docker/containers/${encodeURIComponent(subject.id)}`}>Open container</Link>
        </Button>
      </div>
      {container?.state === "restarting" && (
        <p className="text-hint text-muted-foreground">
          A restart will not stop a crash loop: the lines above say what it fails on. Fix that in
          its configuration or image, then restart.
        </p>
      )}
      {dialog}
    </article>
  )
}

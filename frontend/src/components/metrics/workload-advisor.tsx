"use client"

import Link from "next/link"
import { useState } from "react"
import { get, post, put } from "@/lib/api"
import { bytes, percent, plural, rate, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { ProcessRow } from "@/lib/types"
import type {
  WorkloadControlResult,
  WorkloadGroup,
  WorkloadMember,
  WorkloadReport,
  WorkloadSort,
} from "@/lib/server-advisor"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { CheckCircle, Cpu, Warning } from "@/components/icons"
import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { Disclosure } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Meter } from "@/components/meter"
import { ProductLogo, processProduct } from "@/components/product-logo"
import { ProcessDetailSheet } from "@/components/procs/process-detail"
import { cores, groupName, groupProduct, managerHref, managerName } from "@/components/procs/shared"
import { ChipStrip, FilterChip } from "@/components/tabs"
import type { Tone } from "@/components/tone"
import { Button } from "@/components/ui/button"
import { BorderBeam } from "@/components/ui/border-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { RefreshClockwise } from "@/components/icons"

const SORTS: Record<WorkloadSort, string> = {
  cpu: "CPU",
  memory: "Memory",
  swap: "Swap",
  io: "Disk I/O",
  handles: "Open files",
}

/** One sentence each: what the figure is, and the trap in reading it. */
const EXPLANATIONS: Record<WorkloadSort, string> = {
  cpu: "Measured over a second; a core is 100%. Programs are grouped, so forty renderers are one culprit.",
  memory: "Each group's private memory plus its shared pages once. Stop or restart what grew.",
  swap: "Swapped pages can be idle leftovers; this matters when memory pressure is also high.",
  io: "Disk reads and writes per second. A process blocked on a slow disk can move very little.",
  handles: "Open descriptors per group. A count that only climbs is a leak.",
}

/** How long to let processes exit before measuring the result of a fix. */
const SETTLE_MS = 1500

type Fix = { verb: string; before: number; at: number; hand: boolean; name: string }

/**
 * Who is using the resource the finding is about, grouped the way the
 * Processes page groups them, with the fix each kind of owner takes: a
 * service or a container is restarted through its owner, a program started
 * by hand is stopped — every copy, or what launched them — and anything busy
 * can be lowered in priority. After a fix the group is measured again and
 * the before and after are put side by side.
 */
export function WorkloadAdvisor({
  sort: initialSort,
  onChanged,
}: {
  sort: WorkloadSort
  onChanged: () => void
}) {
  const [sort, setSort] = useState(initialSort)
  const [pid, setPid] = useState<number | null>(null)
  const [fixes, setFixes] = useState<Record<string, Fix>>({})
  const [busy, setBusy] = useState<Record<string, string>>({})
  const { confirm, dialog } = useConfirm()
  const { snapshot } = useMetrics()
  const report = usePoll(
    (signal) => get<WorkloadReport>("/system/advisor/workloads", { sort }, signal),
    10_000,
    [sort],
  )
  const data = report.data
  const groups = data?.groups ?? []
  const top = Math.max(1, ...groups.map((group) => measure(group, sort)))
  const capacity = capacityOf(sort, snapshot)

  // Runs a control against a group, records what it measured before, and
  // measures again once the processes have had a moment to go.
  const fix = async (
    group: WorkloadGroup,
    verb: string,
    hand: boolean,
    run: () => Promise<void>,
  ) => {
    setBusy((current) => ({ ...current, [group.key]: verb }))
    try {
      await run()
      setFixes((current) => ({
        ...current,
        [group.key]: {
          verb,
          before: measure(group, sort),
          at: Date.now(),
          hand,
          name: groupName(group),
        },
      }))
      onChanged()
      setTimeout(report.refresh, SETTLE_MS)
    } catch (err) {
      notify.error(`Could not finish: ${verb.toLowerCase()} ${groupName(group)}`, err)
      throw err
    } finally {
      setBusy((current) => {
        const next = { ...current }
        delete next[group.key]
        return next
      })
    }
  }

  return (
    <section aria-label="Biggest consumers" className="space-y-4">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h3 className="mr-auto text-title font-semibold">Who is using it</h3>
        {data && (
          <span className="numeric text-hint text-muted-foreground">
            {data.total} processes · {timestamp(data.checkedAt)}
          </span>
        )}
        <IconAction label="Measure again" onClick={report.refresh} disabled={report.loading}>
          <RefreshClockwise />
        </IconAction>
      </div>
      <ChipStrip>
        {(Object.keys(SORTS) as WorkloadSort[]).map((key) => (
          <FilterChip key={key} selected={sort === key} onClick={() => setSort(key)}>
            {SORTS[key]}
          </FilterChip>
        ))}
      </ChipStrip>
      <p className="text-body leading-relaxed text-muted-foreground">{EXPLANATIONS[sort]}</p>

      {report.loading && (
        <TextShimmer className="text-body">Sampling every process for a second…</TextShimmer>
      )}
      {report.error && (
        <p role="alert" className="text-body text-destructive">
          {report.error.message} Controls are paused until a fresh reading succeeds.
        </p>
      )}

      {data &&
        Object.entries(fixes)
          .filter(
            ([key, fixed]) =>
              Date.parse(data.checkedAt) > fixed.at && !groups.some((group) => group.key === key),
          )
          .map(([key, fixed]) => (
            <FixOutcome key={key} fixed={fixed} after={undefined} sort={sort} />
          ))}

      {data && (
        <ol className="space-y-2">
          {groups.map((group, index) => (
            <GroupCard
              key={group.key}
              group={group}
              index={index}
              sort={sort}
              share={capacity ? (measure(group, sort) / capacity) * 100 : undefined}
              relative={(measure(group, sort) / top) * 100}
              busy={busy[group.key]}
              fixed={fixes[group.key]}
              checkedAt={data.checkedAt}
              stale={!!report.error}
              confirm={confirm}
              onFix={fix}
              onInspect={setPid}
            />
          ))}
          {!groups.length && (
            <p className="text-body text-muted-foreground">Nothing is using this right now.</p>
          )}
        </ol>
      )}

      {data && (
        <>
          {data.silences.map((reason) => (
            <p key={reason} className="text-hint text-muted-foreground">
              {reason}
            </p>
          ))}
          <ProcessList
            processes={data.processes}
            sort={sort}
            stale={!!report.error}
            confirm={confirm}
            onInspect={setPid}
            onChanged={() => {
              report.refresh()
              onChanged()
            }}
          />
        </>
      )}

      <ProcessDetailSheet
        pid={pid}
        memTotal={snapshot?.memory.total ?? 0}
        onOpenChange={(open) => !open && setPid(null)}
        onSelect={setPid}
        onChanged={() => {
          report.refresh()
          onChanged()
        }}
      />
      {dialog}
    </section>
  )
}

function GroupCard({
  group,
  index,
  sort,
  share,
  relative,
  busy,
  fixed,
  checkedAt,
  stale,
  confirm,
  onFix,
  onInspect,
}: {
  group: WorkloadGroup
  index: number
  sort: WorkloadSort
  /** Of the whole machine, where the measure has a ceiling. */
  share?: number
  /** Of the heaviest group, for the measures that have none. */
  relative: number
  busy?: string
  fixed?: Fix
  checkedAt: string
  stale: boolean
  confirm: (request: ConfirmRequest) => void
  onFix: (
    group: WorkloadGroup,
    verb: string,
    hand: boolean,
    run: () => Promise<void>,
  ) => Promise<void>
  onInspect: (pid: number) => void
}) {
  const { can } = useAuth()
  const name = groupName(group)
  const owner = managerHref({ manager: group.manager, managerName: group.name })
  const hand = group.manager === "session" || !group.manager
  const mark = <ProductLogo id={groupProduct(group)} size="sm" fallback={Cpu} />
  const tone: Tone =
    share !== undefined && share >= 50
      ? "danger"
      : share !== undefined && share >= 25
        ? "warning"
        : "default"
  const settled = fixed && Date.parse(checkedAt) > fixed.at
  // A stop the group shrugged off: the one case a harder signal is the answer.
  const stuck = settled && fixed.hand && measure(group, sort) > fixed.before * 0.5
  const subject = {
    mark,
    name,
    facts: `${plural(group.count, "process", "processes")} · ${group.users.join(", ") || "unknown user"}`,
  }

  const signal = (targets: WorkloadMember[], signalName: "SIGTERM" | "SIGKILL") =>
    post<WorkloadControlResult>("/system/advisor/workloads/signal", {
      targets: targets.map((member) => ({ pid: member.pid, startedAt: member.createTime })),
      signal: signalName,
    }).then((result) => report(result, "signalled"))

  const stopAll = (force: boolean) =>
    confirm({
      title: force ? `Force-stop ${name}` : `Stop ${name}`,
      confirmLabel: force ? "Send SIGKILL" : "Send SIGTERM",
      subject,
      description: (
        <p>
          {force
            ? "Every process in the group is killed at once, without a chance to save or clean up. Use it only when a normal stop was ignored."
            : `Every process in the group is asked to shut down. Work in progress in them stops.`}
          {group.truncated && " The first 256 are signalled; measure again for the rest."}
        </p>
      ),
      action: async () =>
        onFix(group, force ? "Force-stopped" : "Stopped", true, () =>
          signal(group.members, force ? "SIGKILL" : "SIGTERM"),
        ),
    })

  const stopLauncher = () =>
    group.launcher &&
    confirm({
      title: `Stop what started ${name}`,
      confirmLabel: "Send SIGTERM",
      subject: {
        mark: <ProductLogo id={processProduct(group.launcher.name)} size="sm" fallback={Cpu} />,
        name: `${group.launcher.name} · PID ${group.launcher.pid}`,
        facts: group.launcher.username,
      },
      description: (
        <>
          <p>
            This process started the {plural(group.count, "process", "processes")} above. Stopping
            it usually ends them too and keeps it from starting more.
          </p>
          <code className="block font-mono text-xs break-all">{group.launcher.cmdline}</code>
        </>
      ),
      action: async () =>
        onFix(group, "Stopped what started it", true, () =>
          signal([{ pid: group.launcher!.pid, createTime: group.launcher!.createTime }], "SIGTERM"),
        ),
    })

  const restartOwner = () =>
    confirm({
      title: `Restart ${name}`,
      confirmLabel: "Restart",
      subject,
      description: (
        <p>
          {group.manager === "systemd"
            ? "systemd restarts the service. What it held is released, and it comes back fresh."
            : "Docker restarts the container with the same configuration. Requests fail until it is back."}
        </p>
      ),
      action: async () =>
        onFix(group, "Restarted", false, () =>
          post(
            group.manager === "systemd"
              ? `/systemd/${encodeURIComponent(group.name)}/restart`
              : `/docker/containers/${encodeURIComponent(group.name)}/restart`,
          ).then(() => {
            notify.success(`${name} restarted; measuring again`)
          }),
        ),
    })

  const lower = () =>
    onFix(group, "Lowered priority", false, () =>
      post<WorkloadControlResult>("/system/advisor/workloads/priority", {
        targets: group.members.map((member) => ({
          pid: member.pid,
          startedAt: member.createTime,
        })),
        nice: 10,
      }).then((result) => report(result, "lowered")),
    ).catch(() => undefined)

  return (
    <li
      className="relative animate-rise space-y-2.5 rounded-xl border bg-card p-3"
      style={{ animationDelay: `${Math.min(index, 8) * 30}ms` }}
    >
      {busy && (
        <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
          <BorderBeam size={80} duration={4} />
        </span>
      )}
      <div className="flex min-w-0 items-center gap-3">
        {mark}
        <div className="min-w-0 flex-1">
          <p className="flex min-w-0 items-baseline gap-1.5">
            <button
              type="button"
              onClick={() => onInspect(group.pid)}
              className="min-w-0 truncate rounded-sm text-body font-medium focus-ring hover:underline"
            >
              {name}
            </button>
            {group.count > 1 && (
              <span className="numeric shrink-0 text-hint text-muted-foreground">
                ×{group.count}
              </span>
            )}
          </p>
          <p className="truncate text-hint text-muted-foreground">{ownerLine(group)}</p>
        </div>
        <div className="shrink-0 text-right">
          <p
            className={cn(
              "numeric text-title leading-tight font-semibold",
              tone === "danger" && "text-destructive",
              tone === "warning" && "text-warning",
            )}
          >
            {figure(measure(group, sort), sort)}
          </p>
          {share !== undefined && (
            <p className="numeric text-hint text-muted-foreground">
              {percent(Math.min(share, 100), 0)} of the machine
            </p>
          )}
        </div>
      </div>
      <Meter value={share ?? relative} tone={tone} size="thin" label={`${name} share`} />
      {group.cmdline && (
        <p className="truncate font-mono text-hint text-muted-foreground" title={group.cmdline}>
          {group.cmdline}
        </p>
      )}

      {busy && <TextShimmer className="text-body">{`${busy}…`}</TextShimmer>}
      {fixed && !busy && !settled && (
        <TextShimmer className="text-body">{`${fixed.verb} — measuring again…`}</TextShimmer>
      )}
      {fixed && !busy && settled && (
        <FixOutcome fixed={fixed} after={measure(group, sort)} sort={sort} />
      )}

      <div className="flex flex-wrap items-center gap-2">
        {(group.manager === "systemd" || group.manager === "container") && can("destructive") && (
          <Button size="xs" onClick={restartOwner} disabled={stale || !!busy}>
            Restart {group.manager === "systemd" ? "service" : "container"}
          </Button>
        )}
        {hand && group.launcher && can("destructive") && (
          <Button size="xs" onClick={stopLauncher} disabled={stale || !!busy}>
            Stop {group.launcher.name}
          </Button>
        )}
        {hand && can("destructive") && (
          <Button
            size="xs"
            variant={group.launcher ? "outline" : "default"}
            onClick={() => stopAll(false)}
            disabled={stale || !!busy}
          >
            Stop {group.count > 1 ? `all ${group.count}` : "it"}
          </Button>
        )}
        {hand && stuck && can("destructive") && (
          <Button
            size="xs"
            variant="destructive"
            onClick={() => stopAll(true)}
            disabled={stale || !!busy}
          >
            Force stop
          </Button>
        )}
        {(sort === "cpu" || sort === "io") && group.manager !== "kernel" && can("system.admin") && (
          <Button size="xs" variant="outline" onClick={lower} disabled={stale || !!busy}>
            Lower priority
          </Button>
        )}
        {owner && (
          <Button size="xs" variant="ghost" asChild>
            <Link href={owner}>Open {managerName(group.manager).toLowerCase()}</Link>
          </Button>
        )}
        <Button size="xs" variant="ghost" onClick={() => onInspect(group.pid)}>
          Inspect
        </Button>
      </div>
      {group.manager === "pm2" && (
        <p className="text-hint text-muted-foreground">
          PM2 restarts what it supervises — restart or scale the application from PM2.
        </p>
      )}
    </li>
  )

  function report(result: WorkloadControlResult, past: string) {
    const failed = result.items.filter((item) => !item.ok)
    const skipped = result.items.filter((item) => item.skipped).length
    const done = result.items.length - failed.length - skipped
    const already = skipped ? ` (${skipped} already lower)` : ""
    if (failed.length === 0)
      notify.success(`${name}: ${plural(done, "process", "processes")} ${past}${already}`)
    else
      notify.error(
        `${name}: ${done} ${past}, ${failed.length} refused`,
        new Error(failed[0]?.error ?? "refused"),
      )
  }
}

/**
 * What a fix did, read from the measurement taken after it: the group gone or
 * well down is green, the group unchanged is amber with the honest reason.
 */
function FixOutcome({
  fixed,
  after,
  sort,
}: {
  fixed: Fix
  after: number | undefined
  sort: WorkloadSort
}) {
  const better = after === undefined || after <= fixed.before * 0.5
  return (
    <p
      role="status"
      className={cn(
        "flex animate-rise items-center gap-2 rounded-lg border px-3 py-2 text-body",
        better ? "border-rule-success bg-wash-success" : "border-rule-warning bg-wash-warning",
      )}
    >
      {better ? (
        <CheckCircle className="size-4 shrink-0 text-success" />
      ) : (
        <Warning className="size-4 shrink-0 text-warning" />
      )}
      <span className="min-w-0">
        {fixed.verb} {fixed.name}.{" "}
        {after === undefined
          ? `It is gone — it was using ${figure(fixed.before, sort)}.`
          : better
            ? `Now ${figure(after, sort)}, down from ${figure(fixed.before, sort)}.`
            : `Still ${figure(after, sort)} — ${
                fixed.hand
                  ? "it ignored the request or something started it again."
                  : "it came back at the same level."
              }`}
      </span>
    </p>
  )
}

/** The individual processes, for the case a group hides the one that matters. */
function ProcessList({
  processes,
  sort,
  stale,
  confirm,
  onInspect,
  onChanged,
}: {
  processes: ProcessRow[]
  sort: WorkloadSort
  stale: boolean
  confirm: (request: ConfirmRequest) => void
  onInspect: (pid: number) => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  if (!processes.length) return null
  const terminate = (process: ProcessRow) =>
    confirm({
      title: "Terminate process",
      confirmLabel: "Send SIGTERM",
      subject: {
        mark: <ProductLogo id={processProduct(process.name)} size="sm" fallback={Cpu} />,
        name: `${program(process.name)} · PID ${process.pid}`,
        facts: process.username,
      },
      description: (
        <>
          <p>It is asked to shut down; work in progress in it stops.</p>
          <code className="block font-mono text-xs break-all">{process.cmdline}</code>
          {managerHref(process) && (
            <p>
              {managerName(process.manager)} supervises it and may start it again. Use its owner for
              a lasting stop.
            </p>
          )}
        </>
      ),
      action: async () => {
        await post(`/processes/${process.pid}/signal`, {
          signal: "SIGTERM",
          startedAt: process.createTime,
        })
        notify.success(`SIGTERM sent to ${program(process.name)}`)
        onChanged()
        return "reported"
      },
    })
  const lowerPriority = async (process: ProcessRow) => {
    try {
      await put(`/processes/${process.pid}/priority`, {
        nice: Math.min(19, process.nice + 5),
        startedAt: process.createTime,
      })
      notify.success(`${program(process.name)} lowered to nice ${Math.min(19, process.nice + 5)}`)
      onChanged()
    } catch (err) {
      notify.error(`Could not lower ${program(process.name)}`, err)
    }
  }
  return (
    <Disclosure quiet summary="Individual processes" facts={`the ${processes.length} heaviest`}>
      <ul className="divide-y divide-hairline">
        {processes.map((process) => (
          <li
            key={`${process.pid}-${process.createTime}`}
            className="group min-w-0 space-y-1 py-2.5"
          >
            <div className="flex min-w-0 items-center gap-3">
              <button
                type="button"
                onClick={() => onInspect(process.pid)}
                className="min-w-0 truncate rounded-sm text-body font-medium focus-ring hover:underline"
              >
                {program(process.name)}
              </button>
              <span className="numeric shrink-0 text-hint text-muted-foreground">
                {process.pid} · {process.username || "unknown"} · {process.state}
              </span>
              <span className="numeric ml-auto shrink-0 text-body">
                {resourceValue(process, sort)}
              </span>
            </div>
            <p
              className="line-clamp-2 font-mono text-hint break-all text-muted-foreground"
              title={process.cmdline}
            >
              {process.cmdline || "Command unavailable"}
            </p>
            <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
              {can("system.admin") && (sort === "cpu" || sort === "io") && (
                <Button
                  variant="ghost"
                  size="xs"
                  disabled={
                    stale || process.pid < 2 || process.nice >= 19 || !validIdentity(process)
                  }
                  onClick={() => void lowerPriority(process)}
                >
                  Lower priority
                </Button>
              )}
              {can("destructive") && (
                <Button
                  variant="ghost"
                  size="xs"
                  disabled={
                    stale ||
                    process.pid < 2 ||
                    process.manager === "kernel" ||
                    !validIdentity(process)
                  }
                  onClick={() => terminate(process)}
                >
                  Terminate
                </Button>
              )}
            </div>
          </li>
        ))}
      </ul>
    </Disclosure>
  )
}

/** Who a group answers to, in the words the rest of the product uses. */
function ownerLine(group: WorkloadGroup): string {
  const users = group.users.join(", ") || "unknown user"
  switch (group.manager) {
    case "systemd":
      return `systemd service · ${users}`
    case "container":
      return `Container${group.label ? ` ${group.label}` : ""} · ${users}`
    case "pm2":
      return `PM2 application · ${users}`
    case "kernel":
      return "Kernel threads"
    default:
      return group.launcher
        ? `${users} · started by ${group.launcher.name} (PID ${group.launcher.pid})`
        : `${users} · started by hand`
  }
}

function measure(group: WorkloadGroup, sort: WorkloadSort): number {
  switch (sort) {
    case "memory":
      return group.memory
    case "swap":
      return group.swap
    case "io":
      return group.ioRate
    case "handles":
      return group.handles
    default:
      return group.cpuPercent
  }
}

function figure(value: number, sort: WorkloadSort): string {
  switch (sort) {
    case "memory":
    case "swap":
      return bytes(value)
    case "io":
      return rate(value)
    case "handles":
      return `${value.toLocaleString()} open`
    default:
      return cores(value)
  }
}

/** The ceiling a measure is read against, where the machine has one. */
function capacityOf(
  sort: WorkloadSort,
  snapshot: ReturnType<typeof useMetrics>["snapshot"],
): number | undefined {
  if (!snapshot) return undefined
  if (sort === "cpu") return snapshot.cpu.cores * 100
  if (sort === "memory") return snapshot.memory.total || undefined
  if (sort === "swap") return snapshot.swap.total || undefined
  return undefined
}

/** A process's program: Chrome and Node rewrite their names to the whole command line. */
function program(name: string): string {
  const first = name.trim().split(/\s+/)[0] ?? name
  return first.slice(first.lastIndexOf("/") + 1) || name
}

function validIdentity(process: ProcessRow) {
  return Number.isFinite(Date.parse(process.createTime)) && Date.parse(process.createTime) > 0
}

function resourceValue(process: ProcessRow, sort: WorkloadSort) {
  if (sort === "memory") return process.memoryReady ? bytes(process.rss) : "—"
  if (sort === "swap") return process.memoryReady ? bytes(process.swap) : "—"
  if (sort === "handles") return process.fdReady ? `${process.fileDescriptors ?? 0}` : "—"
  if (sort === "io")
    return process.ioReady ? rate((process.ioReadRate ?? 0) + (process.ioWriteRate ?? 0)) : "—"
  return process.cpuReady ? percent(process.cpuPercent) : "—"
}

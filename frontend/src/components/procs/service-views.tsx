"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import {
  Check,
  Copy,
  Cpu,
  Cross,
  External,
  FileText,
  Minus,
  RefreshClockwise,
  Servers,
} from "@/components/icons"
import { get, post, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes, percent, plural } from "@/lib/format"
import { journalSource } from "@/lib/log-sources"
import { notify } from "@/lib/toast"
import type {
  ProcessInspect,
  ProcessList,
  ProcessRow,
  SystemdRelated,
  SystemdSecurity,
  SystemdUnit,
  SystemdUnitFiles,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { EnvironmentTable } from "@/components/docker/container-tables"
import { IconAction } from "@/components/icon-action"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Detail, DetailList, Section } from "@/components/page"
import { Well } from "@/components/panel"
import { ProductLogo, processProduct, unitProduct } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import type { Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { SIGNALS, signalEffect, type SignalSpec } from "@/components/procs/process-facts"
import { UnitMark, relatedState, relatedTone } from "@/components/procs/service-overview"
import {
  RELATIONS,
  RELATION_GROUPS,
  VERDICT_WORDS,
  counter,
  cpuQuotaPercent,
  environmentFiles,
  exposureTone,
  limit,
  parseBytes,
  relatedHref,
  splitEnvironment,
  words,
} from "@/components/procs/service-facts"
import { authUnit, cpuTone, processStateTone } from "@/components/procs/shared"
import { useUnitVerbs, type ConfirmFn } from "@/components/procs/unit-actions"
import { unitRunsView } from "@/components/procs/unit-runs"
import { unitBucket, unitShortName } from "@/components/procs/units"

type Act = Parameters<typeof useUnitVerbs>[0]["act"]

// ---------------------------------------------------------------------------
// Processes

/**
 * Every process in the unit's cgroup, heaviest first, from the process table
 * narrowed to it: a worker pool's spread, the child a service forked that is
 * the one spinning. Each opens its own page, where its signals, files and
 * limits are.
 */
export function ServiceProcesses({
  unit,
  list,
  loading,
  error,
}: {
  unit: SystemdUnit
  list?: ProcessList
  loading: boolean
  error?: Error
}) {
  const running = unitBucket(unit) === "active" || unitBucket(unit) === "changing"
  if (!running) {
    return (
      <EmptyState
        icon={Cpu}
        title="It is not running"
        description="A unit's processes are the ones in its cgroup while it runs. Start it from Control to see them here."
      />
    )
  }
  if (loading && !list) return <LoadingRows rows={4} />
  if (error && !list) return <ErrorState error={error} />
  const rows = list?.processes ?? []
  if (rows.length === 0) {
    return (
      <EmptyState
        icon={Cpu}
        title="No process is running in it"
        description={
          unit.subState === "exited"
            ? "It ran to completion and systemd keeps it active, as it does a oneshot that set RemainAfterExit."
            : "Its cgroup is empty."
        }
      />
    )
  }
  const cpu = rows.reduce((n, row) => n + (row.cpuReady === false ? 0 : row.cpuPercent), 0)
  const rss = rows.reduce((n, row) => n + row.rss, 0)
  return (
    <div className="flex min-w-0 flex-col gap-3 pb-6">
      <p className="numeric text-hint text-muted-foreground">
        {plural(list?.total ?? rows.length, "process", "processes")} · {percent(cpu)} CPU ·{" "}
        {bytes(rss)} resident, busiest first
        {list?.truncated ? ` · showing ${rows.length}` : ""}
      </p>
      <RowList aria-label={`Processes of ${unit.name}`}>
        {rows.map((row) => (
          <UnitProcessRow
            key={row.pid}
            process={row}
            main={row.pid === unit.mainPid}
            ratesReady={list?.ratesReady ?? false}
          />
        ))}
      </RowList>
    </div>
  )
}

function UnitProcessRow({
  process,
  main,
  ratesReady,
}: {
  process: ProcessRow
  main: boolean
  ratesReady: boolean
}) {
  return (
    <Row
      href={`/processes/${process.pid}`}
      mono
      leading={<ProductLogo id={processProduct(process.name)} size="sm" fallback={Cpu} />}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{process.name}</span>
          <span className="numeric font-mono text-hint font-normal text-muted-foreground">
            {process.pid}
          </span>
          {main && <Tag>main</Tag>}
          {process.username && (
            <span className="truncate text-hint font-normal text-muted-foreground">
              {process.username}
            </span>
          )}
        </span>
      }
      subtitle={process.cmdline || "command line not readable"}
      trailing={
        <>
          <span
            className={cn(
              "numeric font-mono text-hint",
              cpuTone(process.cpuPercent) === "default" ? "text-muted-foreground" : "text-warning",
            )}
          >
            {!ratesReady || process.cpuReady === false ? "—" : percent(process.cpuPercent)}
          </span>
          <span className="numeric font-mono text-hint text-muted-foreground">
            {bytes(process.rss)}
          </span>
          <Status state={processStateTone(process.state)} label={process.state} />
        </>
      }
    />
  )
}

// ---------------------------------------------------------------------------
// Journal

/**
 * The unit's journal, read the way the logs page reads it — live, searched,
 * added up through the unit's lens — and its runs beside that: when systemd
 * started it, how long each lasted and how it ended.
 *
 * sshd's journal is login records, which only an administrator reads. For
 * anyone else it opens nothing: the server refuses the read before the
 * socket upgrades.
 */
export function ServiceJournal({ unit }: { unit: string }) {
  const { can } = useAuth()
  const sources = useMemo<ServiceLogSource[]>(
    () => [{ id: journalSource(unit), label: unit, kind: "journal", product: unitProduct(unit) }],
    [unit],
  )
  const views = useMemo(() => [unitRunsView(unit)], [unit])
  if (authUnit(unit) && !can("system.admin")) {
    return (
      <Notice title="Login records need an administrator">
        {unit}&apos;s journal holds every sign-in attempt, and a failed one can hold a password
        typed into the username prompt, so only an administrator can read it.
      </Notice>
    )
  }
  return (
    <ServiceLogs
      sources={sources}
      views={views}
      storageKey={`processes.services.page.logs.${unit}`}
      className="min-h-0 flex-1"
      paneClassName="min-h-[30rem]"
    />
  )
}

// ---------------------------------------------------------------------------
// Dependencies

/**
 * Every unit this one names, under the question each answers — what starts
 * it, what it needs, what it starts, the order, what it cannot run beside —
 * each with its state as the table draws it, so a unit that will not start
 * because something it requires is down shows that something in red. A
 * service opens its page, a timer its schedule.
 */
export function ServiceDependencies({
  properties: p,
  related,
}: {
  properties: Record<string, string>
  related?: SystemdRelated
}) {
  const states = new Map((related?.units ?? []).map((unit) => [unit.name, unit]))
  const groups = RELATION_GROUPS.map((group) => ({
    ...group,
    relations: RELATIONS.filter((r) => r.group === group.key)
      .map((r) => ({ ...r, names: words(p[r.key]) }))
      .filter((r) => r.names.length > 0),
  })).filter((group) => group.relations.length > 0)
  if (groups.length === 0) {
    return (
      <EmptyState
        icon={Servers}
        title="It names no other unit"
        description="Nothing starts it, it needs nothing and it orders itself against nothing."
      />
    )
  }
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      {groups.map((group) => (
        <Section key={group.key} title={group.title}>
          <div className="flex min-w-0 flex-col gap-5">
            {group.relations.map((relation) => (
              <div key={relation.key} className="min-w-0">
                <p className="mb-1 flex min-w-0 items-baseline gap-2">
                  <span className="text-body font-medium">{relation.label}</span>
                  <span className="numeric text-hint text-muted-foreground">
                    {relation.names.length}
                  </span>
                  <span className="truncate text-hint text-muted-foreground">{relation.hint}</span>
                </p>
                <RowList aria-label={relation.label}>
                  {relation.names.map((name) => {
                    const state = states.get(name)
                    const down =
                      (relation.group === "needs" &&
                        relation.key !== "Wants" &&
                        state &&
                        state.activeState !== "active" &&
                        state.activeState !== "activating") ||
                      state?.activeState === "failed"
                    return (
                      <Row
                        key={name}
                        href={relatedHref(name)}
                        leading={<UnitMark name={name} state={state} size="sm" />}
                        title={<span className="font-mono">{name}</span>}
                        subtitle={state?.description !== name ? state?.description : undefined}
                        trailing={
                          state ? (
                            <Status
                              tone={down ? "danger" : relatedTone(state)}
                              label={relatedState(state, true)}
                            />
                          ) : (
                            <span className="text-hint text-muted-foreground">
                              {related ? "not read" : "reading…"}
                            </span>
                          )
                        }
                      />
                    )
                  })}
                </RowList>
              </div>
            ))}
          </div>
        </Section>
      ))}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Unit file

/**
 * The file systemd read the unit from, then each drop-in over it in the order
 * they apply — the unit as it really is, not only as its package shipped it.
 * Each opens in Files to be edited; after an edit systemd needs its unit
 * files re-read, which is offered here as it is on the Overview.
 */
export function ServiceFile({
  unit,
  onDaemonReload,
}: {
  unit: SystemdUnit
  onDaemonReload?: () => void
}) {
  const files = usePoll(
    (signal) =>
      get<SystemdUnitFiles>(`/systemd/${encodeURIComponent(unit.name)}/files`, undefined, signal),
    30_000,
    [unit.name],
  )
  if (!unit.fragmentPath && !files.data?.files.length) {
    return (
      <EmptyState
        icon={FileText}
        title="It has no unit file"
        description="It was made at run time — by a program, a generator or systemd-run — so its settings live only in systemd's memory. They are all under the other views."
      />
    )
  }
  if (files.loading && !files.data) return <LoadingRows rows={6} />
  if (files.error && !files.data) return <ErrorState error={files.error} />
  const list = files.data?.files ?? []
  return (
    <div className="flex min-w-0 flex-col gap-6 pb-6">
      {files.data?.needDaemonReload && (
        <Notice tone="warning" title="Changed on disk since systemd read it">
          <p>
            What is below is the file as it is now; systemd is still running the version it read
            before.
          </p>
          {onDaemonReload && (
            <Button size="xs" variant="outline" className="mt-2" onClick={onDaemonReload}>
              Reload unit files
            </Button>
          )}
        </Notice>
      )}
      {list.map((file) => (
        <Section
          key={file.path}
          title={
            <span className="inline-flex min-w-0 items-center gap-2">
              <span className="truncate font-mono text-body">{file.path}</span>
              {file.dropIn && <Tag>override</Tag>}
            </span>
          }
          actions={
            <span className="flex items-center gap-1">
              <span className="numeric mr-1 text-hint text-muted-foreground">
                {bytes(file.size)}
              </span>
              <IconAction
                label={`Copy ${file.path}`}
                onClick={() => void copyText(file.content, "Unit file copied")}
              >
                <Copy />
              </IconAction>
              <Link
                href={`/files?path=${encodeURIComponent(file.path)}`}
                aria-label={`Open ${file.path} in Files`}
                className="inline-flex size-7 items-center justify-center rounded-md text-muted-foreground focus-ring hover:bg-row-hover hover:text-foreground"
              >
                <External className="size-3.5" />
              </Link>
            </span>
          }
        >
          {file.error ? (
            <EmptyNote>{file.error}</EmptyNote>
          ) : (
            <Well className="overflow-x-auto text-xs leading-relaxed">
              <UnitFileText content={file.content} />
              {file.truncated && (
                <p className="mt-2 text-muted-foreground">
                  # … cut at 256 KiB; open it in Files to read the rest
                </p>
              )}
            </Well>
          )}
        </Section>
      ))}
    </div>
  )
}

/** A unit file coloured as one: sections, keys, values and comments. */
function UnitFileText({ content }: { content: string }) {
  const lines = content.replace(/\n$/, "").split("\n")
  return (
    <pre className="font-mono break-all whitespace-pre-wrap" data-testid="unit-file-text">
      {lines.map((line, i) => {
        const trimmed = line.trimStart()
        let body: React.ReactNode = line
        if (/^[#;]/.test(trimmed)) {
          body = <span className="text-muted-foreground">{line}</span>
        } else if (/^\[.+\]$/.test(trimmed)) {
          body = <span className="font-semibold text-brand">{line}</span>
        } else {
          const eq = line.indexOf("=")
          if (eq > 0) {
            body = (
              <>
                <span className="text-foreground">{line.slice(0, eq)}</span>
                <span className="text-muted-foreground">=</span>
                <span className="text-success">{line.slice(eq + 1)}</span>
              </>
            )
          }
        }
        return (
          <span key={i} className="block min-h-[1lh]">
            {body}
          </span>
        )
      })}
    </pre>
  )
}

// ---------------------------------------------------------------------------
// Environment

/**
 * The environment its unit file gives it (`Environment=`), in the container
 * page's table — credential-shaped values hidden until revealed, and masked
 * by the server for anyone below system.admin — then the files it reads more
 * from and the variables it passes through or drops. What its main process
 * actually holds, those files included, is on that process's page.
 */
export function ServiceEnvironment({
  unit,
  properties: p,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
}) {
  const env = splitEnvironment(p.Environment)
  const files = environmentFiles(p.EnvironmentFiles)
  const pass = words(p.PassEnvironment)
  const unset = words(p.UnsetEnvironment)
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-8 pb-6">
      <div className="flex min-h-0 min-w-0 flex-col gap-3">
        {env.length > 0 ? (
          <EnvironmentTable env={env} />
        ) : (
          <EmptyNote>Its unit file sets no variable of its own.</EmptyNote>
        )}
      </div>
      {(files.length > 0 || pass.length > 0 || unset.length > 0 || unit.mainPid) && (
        <Section title="Also read">
          <DetailList>
            {files.length > 0 && (
              <Detail label={files.length > 1 ? "Files" : "File"}>
                {files.map((file) => (
                  <span key={file.path} className="block">
                    <Link
                      href={`/files?path=${encodeURIComponent(file.path)}`}
                      className="font-mono hover:underline"
                    >
                      {file.path}
                    </Link>
                    {file.optional && (
                      <span className="text-hint text-muted-foreground"> · skipped if missing</span>
                    )}
                  </span>
                ))}
              </Detail>
            )}
            {pass.length > 0 && (
              <Detail label="Passed through" className="font-mono">
                {pass.join(" ")}
              </Detail>
            )}
            {unset.length > 0 && (
              <Detail label="Removed" className="font-mono">
                {unset.join(" ")}
              </Detail>
            )}
            {unit.mainPid ? (
              <Detail label="As it runs">
                <Link
                  href={`/processes/${unit.mainPid}?tab=environment`}
                  className="text-foreground hover:underline"
                >
                  Its main process&apos;s environment, PID {unit.mainPid}
                </Link>
                <span className="block text-hint text-muted-foreground">
                  everything above, the files included, and what systemd adds
                </span>
              </Detail>
            ) : null}
          </DetailList>
        </Section>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Limits and security

/** A cgroup or resource limit in words, with what it is using now where that is known. */
function LimitRow({
  label,
  value,
  used,
  hint,
}: {
  label: string
  value: string
  used?: string
  hint?: string
}) {
  return (
    <Detail label={label}>
      <span className="font-mono">{value}</span>
      {used && <span className="text-muted-foreground"> · {used}</span>}
      {hint && <span className="block text-hint text-muted-foreground">{hint}</span>}
    </Detail>
  )
}

function bytesLimit(value: string | undefined, zero = "none"): string {
  if (!value || value === "infinity") return "no limit"
  const n = limit(value)
  if (n === undefined) return value === "0" ? zero : value
  return bytes(n)
}

/**
 * How much of the host it can reach and how much of it it may use:
 * `systemd-analyze security`'s exposure, 0 when locked down to 10 when
 * nothing is applied, and each setting that counts toward it, most exposed
 * first; then the cgroup's limits with what it uses against each, the
 * per-process limits, and how it is scheduled.
 */
export function ServiceSecurity({
  unit,
  properties: p,
  security,
  loading,
  onTab,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
  security?: SystemdSecurity
  loading: boolean
  onTab: (tab: string) => void
}) {
  const [all, setAll] = useState(false)
  const checks = security?.checks ?? []
  const open = checks.filter((check) => check.set === false && (check.exposure ?? 0) > 0)
  const applied = checks.filter((check) => check.set === true)
  const shown = all ? checks.filter((check) => check.set !== null) : open.slice(0, 12)
  const score = security?.available ? security.score : undefined
  const tone = score !== undefined ? exposureTone(score) : undefined
  const quota = cpuQuotaPercent(p.CPUQuotaPerSecUSec)
  const memory = unit.memoryBytes ?? 0
  const memoryMax = limit(p.MemoryMax)
  const tasksMax = limit(p.TasksMax)
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <Section
        title="What it can reach"
        actions={
          <span className="text-hint text-muted-foreground">from systemd-analyze security</span>
        }
      >
        {loading && !security ? (
          <LoadingRows rows={3} />
        ) : !security?.available ? (
          <EmptyNote>
            {security?.reason ?? "systemd could not assess this unit."} The settings below are read
            from the unit itself.
          </EmptyNote>
        ) : (
          <>
            <StatGrid columns={4} dense data-testid="service-exposure">
              <StatTile
                label="Exposure"
                tone={tone === "danger" ? "danger" : tone === "warning" ? "warning" : "default"}
                value={
                  <span
                    className={cn(
                      tone === "running" && "text-success",
                      tone === "warning" && "text-warning",
                      tone === "danger" && "text-destructive",
                    )}
                  >
                    {score?.toFixed(1)}
                  </span>
                }
                trailing={`of 10 · ${VERDICT_WORDS[security.verdict ?? ""] ?? security.verdict?.toLowerCase() ?? ""}`}
                meter={score !== undefined ? score * 10 : undefined}
                meterLabel="Exposure out of 10"
                hint="lower is better"
              />
              <StatTile
                label="Open"
                tone={open.length > 0 ? "warning" : "default"}
                value={open.length}
                trailing="settings"
                hint="that would lower it"
              />
              <StatTile
                label="Applied"
                value={applied.length}
                trailing="settings"
                hint="already in place"
              />
              <StatTile
                label="Runs as"
                value={
                  <span className="font-mono text-lg">
                    {p.User || (p.DynamicUser === "yes" ? "dynamic" : "root")}
                  </span>
                }
                hint={
                  p.NoNewPrivileges === "yes" ? "cannot gain privileges" : "may gain privileges"
                }
              />
            </StatGrid>
            <ul aria-label="Security settings" className="-mx-2 flex min-w-0 flex-col">
              {shown.map((check) => (
                <li
                  key={check.field}
                  className="flex h-12 min-w-0 items-center gap-3 rounded-md px-2 transition-colors hover:bg-row-hover"
                >
                  <span
                    aria-hidden
                    className={cn(
                      "flex size-5 shrink-0 items-center justify-center rounded-full",
                      check.set
                        ? "bg-plot-success text-success"
                        : (check.exposure ?? 0) >= 0.5
                          ? "bg-plot-danger text-destructive"
                          : "bg-plot-warning text-warning",
                    )}
                  >
                    {check.set ? <Check className="size-3" /> : <Cross className="size-3" />}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-mono text-xs">{check.name}</span>
                    <span
                      className="block truncate text-hint text-muted-foreground"
                      title={check.description}
                    >
                      {check.description}
                    </span>
                  </span>
                  <span className="numeric w-10 shrink-0 text-right font-mono text-hint text-muted-foreground">
                    {check.set || check.exposure === null ? (
                      <Minus aria-label="counts for nothing" className="ml-auto size-3" />
                    ) : (
                      `+${check.exposure.toFixed(1)}`
                    )}
                  </span>
                </li>
              ))}
            </ul>
            {(open.length > 12 || applied.length > 0) && (
              <Button
                size="xs"
                variant="ghost"
                className="self-start"
                onClick={() => setAll((v) => !v)}
              >
                {all
                  ? "Show only what is open"
                  : `Show all ${checks.filter((check) => check.set !== null).length} settings`}
              </Button>
            )}
            <p className="text-hint text-muted-foreground">
              Each is a line in its unit file. Add the ones that suit it to an override from{" "}
              <button
                type="button"
                onClick={() => onTab("file")}
                className="rounded-sm text-foreground focus-ring hover:underline"
              >
                Unit file
              </button>
              , reload the unit files and restart it.
            </p>
          </>
        )}
      </Section>

      <div className="grid min-w-0 gap-8 xl:grid-cols-2 [&>*]:min-w-0">
        <Section title="What it may use">
          <DetailList>
            <LimitRow
              label="Memory"
              value={bytesLimit(p.MemoryMax)}
              used={
                memoryMax
                  ? `${percent((memory / memoryMax) * 100)} used`
                  : memory
                    ? `${bytes(memory)} now`
                    : undefined
              }
              hint="MemoryMax: past it the kernel ends one of its processes"
            />
            {p.MemoryHigh && p.MemoryHigh !== "infinity" && (
              <LimitRow
                label="Memory, soft"
                value={bytesLimit(p.MemoryHigh)}
                hint="MemoryHigh: past it the kernel reclaims hard and slows it"
              />
            )}
            {limit(p.MemoryLow) || limit(p.MemoryMin) ? (
              <LimitRow
                label="Memory, kept"
                value={bytesLimit(p.MemoryMin !== "0" ? p.MemoryMin : p.MemoryLow)}
                hint="MemoryMin / MemoryLow: protected from reclaim"
              />
            ) : null}
            <LimitRow
              label="Swap"
              value={bytesLimit(p.MemorySwapMax, "none allowed")}
              used={
                counter(p.MemorySwapCurrent)
                  ? `${bytes(counter(p.MemorySwapCurrent))} now`
                  : undefined
              }
            />
            <LimitRow
              label="Processor"
              value={quota ? `${quota}% of a core` : "no limit"}
              hint={
                p.CPUWeight && p.CPUWeight !== "[not set]"
                  ? `CPUQuota · weight ${p.CPUWeight} against others`
                  : "CPUQuota"
              }
            />
            <LimitRow
              label="Tasks"
              value={tasksMax ? String(tasksMax) : "no limit"}
              used={unit.tasks ? `${unit.tasks} now` : undefined}
              hint="TasksMax: processes and threads together"
            />
            {p.IOWeight && p.IOWeight !== "[not set]" && (
              <LimitRow label="Disk weight" value={p.IOWeight} hint="IOWeight, 1 to 10000" />
            )}
          </DetailList>
        </Section>
        <Section title="Each of its processes">
          <DetailList>
            <LimitRow label="Open files" value={p.LimitNOFILE || "—"} hint="LimitNOFILE" />
            <LimitRow label="Processes" value={p.LimitNPROC || "—"} hint="LimitNPROC, per user" />
            <LimitRow
              label="Core dumps"
              value={p.LimitCORE === "0" ? "none" : p.LimitCORE || "—"}
              hint="LimitCORE"
            />
            <LimitRow
              label="Locked memory"
              value={limit(p.LimitMEMLOCK) ? bytes(limit(p.LimitMEMLOCK)) : p.LimitMEMLOCK || "—"}
              hint="LimitMEMLOCK"
            />
            <LimitRow
              label="Priority"
              value={`nice ${p.Nice || "0"}`}
              hint={`out-of-memory bias ${p.OOMScoreAdjust || "0"}`}
            />
            <LimitRow
              label="File mask"
              value={p.UMask || "0022"}
              hint="UMask for the files it creates"
            />
          </DetailList>
        </Section>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Control

/** What each lifecycle verb does, said before it is done. */
const VERB_WORDS: Record<string, string> = {
  start: "Starts it now; whether it starts on boot is unchanged.",
  restart: "Stops it and starts it again; whatever it serves waits until it is back.",
  stop: "Stops it until it is started again; it still starts on boot if it is enabled.",
  reload: "Asks it to re-read its configuration without stopping.",
  "reset-failed": "Clears its failed state and its restart count without starting it.",
  freeze: "Pauses every process in it, keeping memory and connections, until it is thawed.",
  thaw: "Lets its frozen processes run again from where they stopped.",
  enable: "Starts it on every boot from the next one.",
  disable: "Stops it starting on boot; it keeps running now.",
  mask: "Links it to /dev/null, so nothing can start it until it is unmasked.",
  unmask: "Lets it be started again, by hand, by another unit and on boot.",
  "daemon-reload": "Re-reads every unit file; needed after one is edited.",
}

const NOW_VERBS = ["start", "restart", "stop", "reload", "reset-failed", "freeze", "thaw"]
const BOOT_VERBS = ["enable", "disable", "mask", "unmask", "daemon-reload"]

/** A verb as a row with what it does, for a list where each is read before it is pressed. */
function VerbRow({ verb, pending }: { verb: Verb; pending?: string }) {
  const Icon = verb.icon
  return (
    <li className="flex h-14 min-w-0 items-center gap-3 rounded-md px-2 transition-colors hover:bg-row-hover">
      {Icon && (
        <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
          <Icon className="size-3.5" />
        </span>
      )}
      <div className="min-w-0 flex-1">
        <p className="truncate text-body font-medium">{verb.label}</p>
        <p className="truncate text-hint text-muted-foreground" title={VERB_WORDS[verb.key]}>
          {VERB_WORDS[verb.key]}
        </p>
      </div>
      <Button
        size="xs"
        variant="outline"
        disabled={Boolean(pending)}
        className={cn(
          (verb.key === "stop" || verb.key === "mask" || verb.key === "freeze") &&
            "text-destructive",
        )}
        onClick={verb.run}
      >
        {verb.label.split(" ")[0]}
      </Button>
    </li>
  )
}

/**
 * Everything that can be done to the unit, each said before it is done:
 * running it now (start, restart, stop, reload, clear a failure, freeze),
 * whether it starts on boot (enable, disable, mask), the signals to its
 * processes — with what each will do to its main process, read from that
 * process's own signal masks — and the limits its cgroup holds it to.
 */
export function ServiceControl({
  unit,
  properties: p,
  confirm,
  act,
  pending,
  onChanged,
  onDaemonReload,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
  confirm: ConfirmFn
  act: Act
  pending?: string
  onChanged: () => void
  onDaemonReload?: () => void
}) {
  const { can } = useAuth()
  const verbs = useUnitVerbs({
    unit,
    confirm,
    act,
    canReload: p.CanReload === "yes",
    // Re-reading the unit files is offered here whether or not this one
    // changed: an edit to another unit it depends on needs it as much.
    properties: { ...p, NeedDaemonReload: "yes" },
    onDaemonReload,
  })
  const now = verbs.filter((verb) => NOW_VERBS.includes(verb.key))
  const boot = verbs.filter((verb) => BOOT_VERBS.includes(verb.key))
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <div className="grid min-w-0 gap-8 xl:grid-cols-2 [&>*]:min-w-0">
        <Section title="Now">
          {now.length === 0 ? (
            <EmptyNote>Running it needs service control.</EmptyNote>
          ) : (
            <ul aria-label="Run it" className="-mx-2 flex min-w-0 flex-col">
              {now.map((verb) => (
                <VerbRow key={verb.key} verb={verb} pending={pending} />
              ))}
            </ul>
          )}
        </Section>
        <Section title="On boot">
          {boot.length === 0 ? (
            <EmptyNote>
              {can("system.admin")
                ? "Its startup is set by another unit; there is nothing to change here."
                : "Changing how it starts needs system administration."}
            </EmptyNote>
          ) : (
            <ul aria-label="Startup" className="-mx-2 flex min-w-0 flex-col">
              {boot.map((verb) => (
                <VerbRow key={verb.key} verb={verb} pending={pending} />
              ))}
            </ul>
          )}
        </Section>
      </div>
      <UnitSignals unit={unit} confirm={confirm} pending={pending} onChanged={onChanged} />
      <UnitLimits unit={unit} properties={p} onChanged={onChanged} />
    </div>
  )
}

/**
 * The signals, sent through systemd (`systemctl kill`) so they reach the
 * unit's processes by its cgroup rather than by a PID that may have moved on.
 * Each says what it will do to the main process, read from that process's
 * own masks; the switch sends it to every process in the unit instead.
 */
function UnitSignals({
  unit,
  confirm,
  pending,
  onChanged,
}: {
  unit: SystemdUnit
  confirm: ConfirmFn
  pending?: string
  onChanged: () => void
}) {
  const { can } = useAuth()
  const [everyone, setEveryone] = useState(false)
  const [sending, setSending] = useState<string>()
  const running = unitBucket(unit) === "active" || unitBucket(unit) === "changing"
  const inspect = usePoll(
    (signal) => get<ProcessInspect>(`/processes/${unit.mainPid}/inspect`, undefined, signal),
    15_000,
    [unit.mainPid],
    { enabled: Boolean(unit.mainPid) },
  )
  const name = unitShortName(unit.name)
  const signalling = running && can("destructive")
  const whom = everyone || !unit.mainPid ? "all" : "main"
  const send = (spec: SignalSpec) => {
    const action = async (phrase?: string) => {
      setSending(spec.signal)
      try {
        await post(
          `/systemd/${encodeURIComponent(unit.name)}/kill`,
          { signal: spec.signal, whom },
          { confirm: phrase },
        )
        notify.success(
          `${spec.signal} sent to ${whom === "main" ? `${unit.name}'s main process` : `every process in ${unit.name}`}`,
        )
        onChanged()
      } catch (err) {
        notify.error(`Could not send ${spec.signal}`, err)
        throw err
      } finally {
        setSending(undefined)
      }
    }
    if (!spec.confirm) {
      void action().catch(() => undefined)
      return
    }
    confirm({
      title: `Send ${spec.signal}`,
      confirmLabel: spec.danger ? spec.label : "Send",
      description: (
        <>
          <p>{signalEffect(spec, name, inspect.data?.signals).sentence}</p>
          <p>
            {whom === "main"
              ? `It goes to its main process, PID ${unit.mainPid}.`
              : `It goes to every process in ${unit.name}${unit.tasks ? `, ${plural(unit.tasks, "task")}` : ""}.`}
            {spec.disposition === "end" || spec.forced
              ? " systemd treats an ending it did not ask for as its restart policy says."
              : ""}
          </p>
        </>
      ),
      action: (phrase) => action(phrase),
    })
  }
  return (
    <Section
      title="Signals"
      actions={
        signalling &&
        unit.mainPid && (
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <Switch
              checked={everyone}
              onCheckedChange={setEveryone}
              aria-label="Send to every process in it"
            />
            To every process in it, not only the main one
          </label>
        )
      }
    >
      {!running && (
        <p className="text-hint text-muted-foreground">
          It is not running, so a signal reaches nothing.
        </p>
      )}
      {running && !can("destructive") && (
        <p className="text-hint text-muted-foreground">
          Sending signals needs destructive actions.
        </p>
      )}
      <ul aria-label="Signals" className="-mx-2 flex min-w-0 flex-col">
        {SIGNALS.map((spec) => {
          const effect = signalEffect(spec, name, inspect.data?.signals)
          return (
            <li
              key={spec.signal}
              className="flex h-14 min-w-0 items-center gap-3 rounded-md px-2 transition-colors hover:bg-row-hover"
            >
              <div className="min-w-0 flex-1">
                <p className="flex min-w-0 items-baseline gap-2">
                  <span className="truncate text-body font-medium">{spec.label}</span>
                  <span className="shrink-0 font-mono text-hint text-muted-foreground">
                    {spec.signal}
                  </span>
                  <span
                    className={cn(
                      "shrink-0 text-hint font-medium",
                      effect.tone === "success" && "text-success",
                      effect.tone === "warning" && "text-warning",
                      effect.tone === "danger" && "text-destructive",
                      effect.tone === "muted" && "text-muted-foreground",
                    )}
                  >
                    {effect.word}
                  </span>
                </p>
                <p className="truncate text-hint text-muted-foreground" title={effect.sentence}>
                  {effect.tone === "success" ? spec.usual : effect.sentence}
                </p>
              </div>
              <Button
                size="xs"
                variant="outline"
                disabled={!signalling || Boolean(pending) || sending !== undefined}
                className={cn(spec.danger && "text-destructive")}
                aria-label={`Send ${spec.signal} to ${unit.name}`}
                onClick={() => send(spec)}
              >
                {sending === spec.signal ? "Sending…" : "Send"}
              </Button>
            </li>
          )
        })}
      </ul>
    </Section>
  )
}

/**
 * The limits its cgroup holds it to, set with `systemctl set-property`: until
 * the next boot, or kept in a drop-in systemd writes for it. Each field is
 * the setting's current value; emptying one lifts it.
 */
function UnitLimits({
  unit,
  properties: p,
  onChanged,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
  onChanged: () => void
}) {
  const { can } = useAuth()
  const currentMemory = limit(p.MemoryMax)
  const currentQuota = cpuQuotaPercent(p.CPUQuotaPerSecUSec)
  const currentTasks = limit(p.TasksMax)
  const [memory, setMemory] = useState(currentMemory ? bytes(currentMemory).replace(/\s/g, "") : "")
  const [quota, setQuota] = useState(currentQuota ? String(currentQuota) : "")
  const [tasks, setTasks] = useState(currentTasks ? String(currentTasks) : "")
  const [persistent, setPersistent] = useState(true)
  const [saving, setSaving] = useState(false)
  const admin = can("system.admin")

  const memoryBytes = memory.trim() ? parseBytes(memory) : undefined
  const quotaValue = quota.trim() ? Number(quota.replace(/%$/, "")) : undefined
  const tasksValue = tasks.trim() ? Number(tasks) : undefined
  const invalid =
    (memory.trim() !== "" && memoryBytes === undefined) ||
    (quota.trim() !== "" && !(Number.isInteger(quotaValue) && (quotaValue ?? 0) > 0)) ||
    (tasks.trim() !== "" && !(Number.isInteger(tasksValue) && (tasksValue ?? 0) > 0))
  const body: Record<string, string | boolean> = { persistent }
  if ((memoryBytes ?? 0) !== (currentMemory ?? 0))
    body.memoryMax = memoryBytes ? String(memoryBytes) : "infinity"
  if ((quotaValue ?? 0) !== (currentQuota ?? 0)) body.cpuQuota = quotaValue ? `${quotaValue}%` : ""
  if ((tasksValue ?? 0) !== (currentTasks ?? 0))
    body.tasksMax = tasksValue ? String(tasksValue) : "infinity"
  const changed = Object.keys(body).length > 1

  const save = async () => {
    setSaving(true)
    try {
      await put(`/systemd/${encodeURIComponent(unit.name)}/limits`, body)
      notify.success(`${unit.name}'s limits ${persistent ? "set" : "set until the next boot"}`)
      onChanged()
    } catch (err) {
      notify.error("Could not set its limits", err)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Section title="Limits">
      {!admin && (
        <p className="text-hint text-muted-foreground">
          Setting its limits needs system administration.
        </p>
      )}
      <form
        aria-label="Resource limits"
        className="grid min-w-0 gap-4 sm:grid-cols-3"
        onSubmit={(event) => {
          event.preventDefault()
          if (changed && !invalid) void save()
        }}
      >
        <LimitField
          id="limit-memory"
          label="Memory"
          hint={
            memoryBytes
              ? `${bytes(memoryBytes)} · ${bytes(unit.memoryBytes ?? 0)} used now`
              : `empty for no limit · ${bytes(unit.memoryBytes ?? 0)} used now`
          }
          placeholder="no limit"
          value={memory}
          onChange={setMemory}
          disabled={!admin}
          invalid={memory.trim() !== "" && memoryBytes === undefined}
        />
        <LimitField
          id="limit-cpu"
          label="Processor, % of a core"
          hint="200 is two cores; empty for no limit"
          placeholder="no limit"
          value={quota}
          onChange={setQuota}
          disabled={!admin}
          invalid={quota.trim() !== "" && !(Number.isInteger(quotaValue) && (quotaValue ?? 0) > 0)}
        />
        <LimitField
          id="limit-tasks"
          label="Tasks"
          hint={`processes and threads · ${unit.tasks ?? 0} now`}
          placeholder="no limit"
          value={tasks}
          onChange={setTasks}
          disabled={!admin}
          invalid={tasks.trim() !== "" && !(Number.isInteger(tasksValue) && (tasksValue ?? 0) > 0)}
        />
        <div className="flex flex-wrap items-center gap-4 sm:col-span-3">
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <Switch
              checked={persistent}
              onCheckedChange={setPersistent}
              disabled={!admin}
              aria-label="Keep after a reboot"
            />
            {persistent
              ? "Kept after a reboot, in a drop-in systemd writes"
              : "Until the next boot only"}
          </label>
          <Button
            type="submit"
            size="sm"
            className="ml-auto"
            disabled={!admin || !changed || invalid || saving}
          >
            <RefreshClockwise className="size-3.5" />
            {saving ? "Setting…" : "Set limits"}
          </Button>
        </div>
      </form>
    </Section>
  )
}

function LimitField({
  id,
  label,
  hint,
  placeholder,
  value,
  onChange,
  disabled,
  invalid,
}: {
  id: string
  label: string
  hint: string
  placeholder: string
  value: string
  onChange: (value: string) => void
  disabled: boolean
  invalid: boolean
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <label htmlFor={id} className="text-xs font-medium">
        {label}
      </label>
      <Input
        id={id}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        aria-invalid={invalid || undefined}
        onChange={(event) => onChange(event.target.value)}
        className="font-mono"
      />
      <p
        className={cn("truncate text-hint", invalid ? "text-destructive" : "text-muted-foreground")}
      >
        {invalid ? "Not a size this takes" : hint}
      </p>
    </div>
  )
}

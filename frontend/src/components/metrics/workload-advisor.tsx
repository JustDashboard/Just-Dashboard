"use client"

import Link from "next/link"
import { useState } from "react"
import { get, post, put } from "@/lib/api"
import { bytes, percent, rate, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ProcessRow } from "@/lib/types"
import type { WorkloadReport, WorkloadSort } from "@/lib/server-advisor"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Panel, PanelHeader, PanelBody } from "@/components/panel"
import { ProcessDetailSheet } from "@/components/procs/process-detail"
import { managerHref, managerName } from "@/components/procs/shared"
import { FilterChip } from "@/components/tabs"

const SORTS: Record<WorkloadSort, string> = {
  cpu: "CPU",
  memory: "Memory",
  swap: "Swap",
  io: "Disk I/O",
  handles: "Open files",
}
const EXPLANATIONS: Record<WorkloadSort, string> = {
  cpu: "CPU uses two measured counters; 100% means one core. High load also includes tasks waiting on I/O, so inspect Disk I/O and blocked process states when CPU alone does not explain it.",
  memory:
    "Resident memory is held in RAM. Shared pages can appear in several processes, so these figures cannot be added to recover host memory usage. Stop or limit the workload that grew after reviewing its owner.",
  swap: "These processes have swapped pages. Occupied swap can hold idle pages without active thrashing; compare memory pressure and disk activity before stopping anything. Disabling swap under memory pressure can trigger OOM kills.",
  io: "These rates count actual disk reads and writes over measured intervals. A blocked process can show little throughput while waiting on a slow device; inspect its owner, files and device latency.",
  handles:
    "Counts are open descriptors per process. They help locate leaks but differ from the host's open-file descriptions, which can be shared by several descriptors. Inspect the process's own limit before changing it.",
}

export function WorkloadAdvisor({
  sort: initialSort,
  onChanged,
}: {
  sort: WorkloadSort
  onChanged: () => void
}) {
  const [sort, setSort] = useState(initialSort)
  const [pid, setPid] = useState<number | null>(null)
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { snapshot } = useMetrics()
  const report = usePoll(
    (signal) => get<WorkloadReport>("/system/advisor/workloads", { sort }, signal),
    10_000,
    [sort],
  )
  const refresh = () => {
    report.refresh()
    onChanged()
  }
  const terminate = (process: ProcessRow) =>
    confirm({
      title: "Terminate process",
      confirmLabel: "Send SIGTERM",
      description: (
        <>
          <p>
            {process.name} (PID {process.pid}) will be asked to shut down. This may interrupt
            requests or work in progress. Review its command and owner first.
          </p>
          <code className="block break-all">{process.cmdline}</code>
          {managerHref(process) && (
            <p>
              {managerName(process.manager)} supervises this process and may start it again. Use its
              owner controls for a lasting stop.
            </p>
          )}
        </>
      ),
      action: async () => {
        await post(`/processes/${process.pid}/signal`, {
          signal: "SIGTERM",
          startedAt: process.createTime,
        })
        notify.success(`SIGTERM sent to ${process.name}; checking the workload again`)
        refresh()
        return "reported"
      },
    })
  const lowerPriority = (process: ProcessRow) =>
    confirm({
      title: "Lower process priority",
      confirmLabel: "Lower priority",
      description: (
        <p>
          Set {process.name} (PID {process.pid}) from nice {process.nice} to{" "}
          {Math.min(19, process.nice + 5)}. This lets competing work run sooner. It does not cap
          CPU, release memory, or change the owner’s launch configuration.
        </p>
      ),
      action: async () => {
        await put(`/processes/${process.pid}/priority`, {
          nice: Math.min(19, process.nice + 5),
          startedAt: process.createTime,
        })
        refresh()
      },
    })
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        {(Object.keys(SORTS) as WorkloadSort[]).map((key) => (
          <FilterChip key={key} selected={sort === key} onClick={() => setSort(key)}>
            {SORTS[key]}
          </FilterChip>
        ))}
        <Button variant="outline" size="sm" className="ml-auto" onClick={report.refresh}>
          Measure again
        </Button>
      </div>
      <p className="text-body leading-relaxed text-muted-foreground">{EXPLANATIONS[sort]}</p>
      {report.loading && (
        <p role="status" className="text-body text-muted-foreground">
          Sampling process activity…
        </p>
      )}
      {report.error && (
        <p role="alert" className="text-body text-destructive">
          {report.error.message} Controls are paused until a fresh reading succeeds.
        </p>
      )}
      {report.data && (
        <Panel plain>
          <PanelHeader
            title="Largest consumers"
            actions={
              <span className="text-hint text-muted-foreground">
                Top {report.data.processes.length} of {report.data.total} ·{" "}
                {timestamp(report.data.checkedAt)}
              </span>
            }
          />
          <PanelBody className="space-y-3">
            {report.data.silences.map((reason) => (
              <p key={reason} className="text-hint text-muted-foreground">
                {reason}
              </p>
            ))}
            <div className="divide-y divide-hairline">
              {report.data.processes.map((process) => (
                <div
                  key={`${process.pid}-${process.createTime}`}
                  className="min-w-0 space-y-2 py-3"
                >
                  <div className="flex min-w-0 items-start gap-3">
                    <div className="min-w-0 flex-1">
                      <button
                        className="text-body font-medium focus-ring hover:underline"
                        onClick={() => setPid(process.pid)}
                      >
                        {process.name} · {process.pid}
                      </button>
                      <p className="text-hint break-all text-muted-foreground">
                        {process.username || "Unknown user"} · {process.state} ·{" "}
                        {process.cmdline || "Command unavailable"}
                      </p>
                    </div>
                    <span className="numeric shrink-0 text-body">
                      {resourceValue(process, sort)}
                    </span>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Button variant="outline" size="xs" onClick={() => setPid(process.pid)}>
                      Inspect
                    </Button>
                    {managerHref(process) && (
                      <Button variant="outline" size="xs" asChild>
                        <Link href={managerHref(process)!}>
                          Manage {managerName(process.manager)}
                        </Link>
                      </Button>
                    )}
                    {can("system.admin") && sort === "cpu" && (
                      <Button
                        variant="outline"
                        size="xs"
                        disabled={
                          !!report.error ||
                          process.pid < 2 ||
                          process.nice >= 19 ||
                          !validIdentity(process)
                        }
                        onClick={() => lowerPriority(process)}
                      >
                        Lower priority
                      </Button>
                    )}
                    {can("destructive") && (
                      <Button
                        variant="outline"
                        size="xs"
                        disabled={
                          !!report.error ||
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
                </div>
              ))}
            </div>
            {!report.data.processes.length && (
              <p className="text-body text-muted-foreground">
                No processes were returned by this host.
              </p>
            )}
          </PanelBody>
        </Panel>
      )}
      <ProcessDetailSheet
        pid={pid}
        memTotal={snapshot?.memory.total ?? 0}
        onOpenChange={(open) => !open && setPid(null)}
        onSelect={setPid}
        onChanged={refresh}
      />
      {dialog}
    </div>
  )
}

function validIdentity(process: ProcessRow) {
  return Number.isFinite(Date.parse(process.createTime)) && Date.parse(process.createTime) > 0
}
function resourceValue(process: ProcessRow, sort: WorkloadSort) {
  if (sort === "memory") return process.memoryReady ? bytes(process.rss) : "Unavailable"
  if (sort === "swap") return process.memoryReady ? bytes(process.swap) : "Unavailable"
  if (sort === "handles")
    return process.fdReady ? `${process.fileDescriptors ?? 0} descriptors` : "Unavailable"
  if (sort === "io")
    return process.ioReady
      ? rate((process.ioReadRate ?? 0) + (process.ioWriteRate ?? 0))
      : "Sampling unavailable"
  return process.cpuReady
    ? `${percent(process.cpuPercent)} · ${process.cpuWindowSeconds?.toFixed(1)}s`
    : "Sampling unavailable"
}

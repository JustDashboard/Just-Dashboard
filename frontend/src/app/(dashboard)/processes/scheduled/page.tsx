"use client"

import { useState } from "react"
import Link from "next/link"
import { Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { describeCron } from "@/lib/cron"
import type { Crontab } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useViewState } from "@/lib/view-state"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageHeader } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { CronJobsPanel } from "@/components/procs/cron-jobs"
import { TimersPanel } from "@/components/procs/timers"
import { cn } from "@/lib/utils"

/**
 * Everything that runs on a clock: one account's crontab as jobs you can
 * act on, the host's systemd timers, and the package-owned cron files,
 * which are read-only here because editing them belongs to the package
 * manager.
 */
export default function ScheduledPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [user, setUser] = useViewState("processes.cron.user", "root")
  const [adding, setAdding] = useState(false)
  const users = usePoll((signal) => get<string[]>("/cron/users", undefined, signal), 0)
  const system = usePoll((signal) => get<Crontab[]>("/cron/system", undefined, signal), 0)

  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Processes"
        title="Scheduled"
        actions={
          can("system.admin") && (
            <Button size="sm" onClick={() => setAdding(true)}>
              <Plus className="size-3.5" />
              Add job
            </Button>
          )
        }
      />

      <CronJobsPanel
        user={user}
        users={users.data ?? []}
        onUserChange={setUser}
        confirm={confirm}
        adding={adding}
        onAddingChange={setAdding}
      />

      <TimersPanel confirm={confirm} />

      <Panel plain>
        <PanelHeader title="System cron files" advanced />
        <PanelBody className="space-y-6">
          {system.data?.map((file) => (
            <section key={file.source} className="min-w-0 space-y-2">
              <p className="eyebrow">
                <Link
                  href={`/files?path=${encodeURIComponent(file.source)}`}
                  className="font-mono tracking-normal normal-case hover:underline"
                >
                  {file.source}
                </Link>
              </p>
              {file.jobs.length === 0 ? (
                <EmptyNote className="py-2 text-left">No jobs.</EmptyNote>
              ) : (
                <Table containerClassName="-mx-4 w-auto">
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-52">Schedule</TableHead>
                      <TableHead className="w-24">User</TableHead>
                      <TableHead className="w-full">Command</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {file.jobs.map((job, i) => (
                      <TableRow
                        key={i}
                        // A commented-out schedule is real syntax, not a
                        // running job — /etc/crontab ships one as its own
                        // worked example. Dimming it keeps the two apart.
                        className={cn(job.disabled && "opacity-55")}
                      >
                        <TableCell className="whitespace-normal">
                          <div className="min-w-44">
                            <p className="font-mono whitespace-nowrap">
                              {job.disabled && (
                                <span className="mr-1 text-muted-foreground">#</span>
                              )}
                              {job.schedule}
                            </p>
                            <p className="text-hint text-muted-foreground">
                              {describeCron(job.schedule)}
                            </p>
                          </div>
                        </TableCell>
                        <TableCell className="text-muted-foreground">{job.user || "—"}</TableCell>
                        <TableCell className="whitespace-normal">
                          <p className="font-mono text-xs break-all">{job.command}</p>
                          {job.comment && (
                            <p className="text-hint text-muted-foreground">{job.comment}</p>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </section>
          ))}
          {system.data && system.data.length === 0 && <EmptyNote>No system cron files.</EmptyNote>}
        </PanelBody>
      </Panel>
      {dialog}
    </Page>
  )
}

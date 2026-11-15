"use client"

import Link from "next/link"
import { Clock } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import type { AuditEntry } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { CHANGE_FAMILIES, changeWords, deviceChanges } from "./device-facts"

/** The trail answers a family at a time; this many of its newest entries are read. */
const READ = 300

/**
 * What was done to a device from the dashboard: made, set up or down, its
 * MTU, its bridge, its VLANs and its addresses, each with who did it and
 * whether the server accepted it. Read from the audit trail's network
 * entries, which only an administrator may read, and narrowed to this
 * device here: the trail is not asked by target.
 */
export function useDeviceHistory(name: string, enabled: boolean) {
  const audit = usePoll(
    (signal) =>
      get<{ entries?: AuditEntry[] }>(
        "/audit/",
        { action_prefix: CHANGE_FAMILIES, limit: READ },
        signal,
      ),
    60_000,
    [name],
    { enabled },
  )
  return { ...audit, changes: audit.data ? deviceChanges(audit.data.entries, name) : undefined }
}

export function DeviceHistory({
  name,
  history,
}: {
  name: string
  history: ReturnType<typeof useDeviceHistory>
}) {
  const { changes } = history
  return (
    <Panel plain className="pb-6">
      <PanelHeader
        title="Changes made here"
        actions={
          changes && (
            <span className="numeric text-hint text-muted-foreground">{changes.length}</span>
          )
        }
      />
      <PanelBody>
        {!changes ? (
          history.error ? (
            <ErrorState error={history.error} onRetry={history.refresh} />
          ) : (
            <LoadingRows rows={4} />
          )
        ) : changes.length === 0 ? (
          <EmptyState
            icon={Clock}
            title="Nothing was changed from the dashboard"
            description={
              <>
                Setting <span className="font-mono">{name}</span> up or down, its MTU, its bridge
                and its addresses are recorded here once somebody changes them.
              </>
            }
            action={
              <Link
                href="/audit"
                className="rounded-sm text-body font-medium text-brand focus-ring hover:underline"
              >
                Open the audit log
              </Link>
            }
          />
        ) : (
          <ol aria-label="Changes made here" className="animate-rise">
            {changes.map((entry) => {
              const words = changeWords(entry)
              return (
                <li
                  key={entry.id}
                  data-slot="history-row"
                  className="flex h-11 min-w-0 items-center gap-3 border-b border-hairline text-body last:border-b-0"
                >
                  <StatusDot tone={entry.success ? "running" : "danger"} />
                  <span
                    className={cn("shrink-0 font-medium", !entry.success && "text-destructive")}
                  >
                    {words.title}
                    {!entry.success && " (refused)"}
                  </span>
                  <span className="min-w-0 flex-1 truncate font-mono text-hint text-muted-foreground">
                    {words.value}
                  </span>
                  <span className="hidden shrink-0 text-hint text-muted-foreground sm:inline">
                    {entry.username || entry.actor}
                  </span>
                  <time
                    dateTime={entry.ts}
                    title={timestamp(entry.ts)}
                    className="numeric w-20 shrink-0 text-right text-hint text-muted-foreground"
                  >
                    {relativeTime(entry.ts)}
                  </time>
                </li>
              )
            })}
          </ol>
        )}
      </PanelBody>
    </Panel>
  )
}

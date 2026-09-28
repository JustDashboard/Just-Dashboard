"use client"

import Link from "next/link"
import { ArrowRight } from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { AuditEntry } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Skeleton } from "@/components/ui/skeleton"
import { ActionMark, ActionName, actorName } from "@/components/audit/marks"
import { cn } from "@/lib/utils"

/**
 * The audit families that change what the proxy serves: its own config and
 * engine, the certificates, and the packages nginx and certbot arrive
 * through. Repeated rather than joined, so the server matches each as a
 * prefix and a certificate action never matches a substring elsewhere.
 */
const FAMILIES = ["proxy.", "certificates.", "system.packages."]
const QUERY = FAMILIES.map((p) => `action_prefix=${encodeURIComponent(p)}`).join("&")

/**
 * What was done to the proxy lately, and by whom, from the audit log. A
 * failure is said as one in its row: a reload nginx refused or a renewal
 * that did not go through is the change most worth seeing here. Read by
 * the accounts that may read the audit log, which is system.admin.
 */
export function RecentChanges() {
  const activity = usePoll(
    (signal) =>
      get<{ entries: AuditEntry[]; total: number }>(`/audit/?${QUERY}&limit=8`, undefined, signal),
    60_000,
  )
  const entries = activity.data?.entries

  return (
    <Panel plain>
      <PanelHeader
        title="Recent changes"
        actions={
          <Link
            href="/audit"
            className="flex items-center gap-1 text-hint font-medium text-muted-foreground hover:text-foreground"
          >
            Audit log <ArrowRight className="size-3" />
          </Link>
        }
      />
      <PanelBody flush>
        {activity.error ? (
          <p className="text-hint text-muted-foreground" title={errorMessage(activity.error)}>
            {"Couldn't read the audit log."}
          </p>
        ) : !entries ? (
          <div className="space-y-3 py-1">
            <Skeleton className="h-4 w-56" />
            <Skeleton className="h-4 w-40" />
          </div>
        ) : entries.length === 0 ? (
          <p className="text-hint text-muted-foreground">
            No proxy, certificate or package changes recorded yet.
          </p>
        ) : (
          <ul aria-label="Recent changes" className="animate-rise divide-y divide-hairline">
            {entries.map((entry) => (
              <li key={entry.id} className="flex min-w-0 items-start gap-3 py-2.5">
                <ActionMark action={entry.action} />
                <div className="min-w-0 flex-1 space-y-0.5">
                  <div className="flex min-w-0 items-center justify-between gap-2">
                    <ActionName action={entry.action} className="text-xs" />
                    {!entry.success && <Status tone="danger" label={`Failed · ${entry.status}`} />}
                  </div>
                  {entry.target && (
                    <p
                      className={cn(
                        "truncate font-mono text-hint",
                        entry.success ? "text-muted-foreground" : "text-foreground",
                      )}
                      title={entry.target}
                    >
                      {entry.target}
                    </p>
                  )}
                  <p className="text-hint text-muted-foreground">
                    {actorName(entry)} · {relativeTime(entry.ts)}
                  </p>
                </div>
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}

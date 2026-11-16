"use client"

import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { AuditEntry } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState } from "@/components/state"
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
 *
 * Five, two lines each: it sits in the rail beside the routes, and eight
 * three-line rows ran the rail a screen past the routes it stands beside,
 * leaving the wide column empty all the way down. The rest are the audit
 * log's.
 */
export function RecentChanges() {
  const activity = usePoll(
    (signal) =>
      get<{ entries: AuditEntry[]; total: number }>(`/audit/?${QUERY}&limit=5`, undefined, signal),
    60_000,
  )
  const entries = activity.data?.entries

  return (
    <Panel plain>
      <PanelHeader title="Recent changes" link={{ href: "/audit", label: "Audit log" }} />
      <PanelBody flush>
        {activity.error && !entries ? (
          // A failed read is a failure with the way to ask again, not an
          // empty list in grey with its reason in a tooltip.
          <ErrorState
            error={activity.error}
            what="the audit log"
            onRetry={activity.refresh}
            className="mt-3"
          />
        ) : !entries ? (
          <div className="space-y-3 py-1">
            <Skeleton className="h-4 w-56" />
            <Skeleton className="h-4 w-40" />
          </div>
        ) : entries.length === 0 ? (
          <EmptyNote dense className="text-left">
            No proxy, certificate or package changes recorded yet.
          </EmptyNote>
        ) : (
          <ul aria-label="Recent changes" className="animate-rise divide-y divide-hairline">
            {entries.map((entry) => (
              <li key={entry.id} className="flex min-w-0 items-center gap-3 py-2">
                <ActionMark action={entry.action} />
                <div className="min-w-0 flex-1 space-y-0.5">
                  <div className="flex min-w-0 items-center justify-between gap-2">
                    <ActionName action={entry.action} className="min-w-0 truncate text-xs" />
                    {entry.success ? (
                      <span className="numeric shrink-0 text-hint text-muted-foreground">
                        {relativeTime(entry.ts)}
                      </span>
                    ) : (
                      <Status tone="danger" label={`Failed · ${entry.status}`} />
                    )}
                  </div>
                  <p
                    className="truncate text-hint text-muted-foreground"
                    title={entry.target ? `${entry.target} · ${actorName(entry)}` : undefined}
                  >
                    {entry.target && (
                      <span className={cn("font-mono", !entry.success && "text-foreground")}>
                        {entry.target}
                      </span>
                    )}
                    {entry.target && " · "}
                    {actorName(entry)}
                    {!entry.success && ` · ${relativeTime(entry.ts)}`}
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

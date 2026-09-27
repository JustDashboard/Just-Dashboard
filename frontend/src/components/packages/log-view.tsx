"use client"

import { useEffect, useMemo } from "react"
import { Logs } from "@/components/icons"
import { get } from "@/lib/api"
import type { LogSourceIndex } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import type { LogTimeRange } from "@/components/logs/types"
import { ServiceLogs } from "@/components/logs/service-logs"
import { packageLogSources } from "@/components/packages/package-logs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"

/** Where the view's reading is kept for the tab. */
const STORAGE_KEY = "packages.log"

/**
 * What the package manager did, read back from its own logs: apt's
 * transactions with the command and the account behind each, dpkg's
 * per-package record, the unattended runs — installs, upgrades and removals
 * as events, and Insights ranking the packages that moved. History and
 * Insights only: a package log is written a few times a week, and a live
 * tail of it is an empty pane.
 *
 * Named Log rather than History because the pane's own first tab is History,
 * and one strip over another both saying History is two places with one name.
 */
export function PackageLogView({ product }: { product?: string }) {
  const index = usePoll<LogSourceIndex>((signal) => get("/logs/sources", undefined, signal), 0)
  const sources = useMemo(
    () => (index.data ? packageLogSources(index.data, product) : undefined),
    [index.data, product],
  )
  const opened = useOpeningRange(STORAGE_KEY, "all")

  if (index.error && !index.data) {
    return <ErrorState error={index.error} onRetry={index.refresh} />
  }
  if (!sources || !opened) return <LoadingPanel />
  if (sources.length === 0) {
    return (
      <EmptyState
        icon={Logs}
        title="No package log on this host"
        description={`apt keeps its transactions in /var/log/apt/history.log, dpkg in /var/log/dpkg.log and dnf in /var/log/dnf.log, and none of them is among the files the dashboard may read under ${index.data?.roots.join(", ") || "its log roots"}.`}
      />
    )
  }
  return (
    <ServiceLogs
      sources={sources}
      storageKey={STORAGE_KEY}
      modes={["search", "insights"]}
      pickerLabel="Package log"
      paneClassName="h-[min(75vh,40rem)] min-h-80"
    />
  )
}

/**
 * The window History first opens on. `ServiceLogs` opens every source on the
 * last day and takes no other, and a package log a day long is usually empty
 * — so, once per tab, the range it keeps under this key is written first as
 * everything the live file holds, a month of apt's transactions. The reader's
 * own choice is what is kept after that.
 */
function useOpeningRange(storageKey: string, range: LogTimeRange) {
  const [kept, setKept] = useSessionState<string>(`${storageKey}.range`, "")
  useEffect(() => {
    if (kept === "") setKept(range)
  }, [kept, range, setKept])
  return kept !== ""
}

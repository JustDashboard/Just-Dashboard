"use client"

import { useEffect, useMemo } from "react"
import { Logs } from "@/components/icons"
import { useSessionState } from "@/lib/view-state"
import type { LogTimeRange } from "@/components/logs/types"
import { ServiceLogs } from "@/components/logs/service-logs"
import {
  PACKAGE_LOGS,
  packageLogSources,
  packageLogsOutsideRoots,
} from "@/components/packages/package-logs"
import { useLogFiles } from "@/components/security/log-section"
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
  const files = useLogFiles(PACKAGE_LOGS)
  const sources = useMemo(
    () => (files.data ? packageLogSources(files.data, product) : undefined),
    [files.data, product],
  )
  const opened = useOpeningRange(STORAGE_KEY, "all")

  if (files.error && !files.data) {
    return <ErrorState error={files.error} onRetry={files.refresh} />
  }
  if (!sources || !opened) return <LoadingPanel />
  if (sources.length === 0) {
    return files.data && packageLogsOutsideRoots(files.data) ? (
      <EmptyState
        icon={Logs}
        title="The package logs are outside JD_LOG_ROOTS"
        description="apt, dpkg and dnf write under /var/log, and the dashboard reads logs only inside the directories JD_LOG_ROOTS names. Add /var/log to it to read them here."
      />
    ) : (
      <EmptyState
        icon={Logs}
        title="No package log on this host"
        description="apt keeps its transactions in /var/log/apt/history.log, dpkg in /var/log/dpkg.log and dnf in /var/log/dnf.log, and none of them is on this host."
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

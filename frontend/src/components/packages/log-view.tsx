"use client"

import { useMemo } from "react"
import { Logs } from "@/components/icons"
import { ServiceLogs } from "@/components/logs/service-logs"
import {
  PACKAGE_LOGS,
  packageLogSources,
  packageLogsOutsideRoots,
} from "@/components/packages/package-logs"
import { useLogFiles } from "@/components/security/log-section"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"

/** Where the view's reading is kept for the tab: the list's, and a package's own. */
const STORAGE_KEY = "packages.log"
const ABOUT_STORAGE_KEY = "packages.page.log"

/** The logs that record one line per package, read first when the view is about one. */
const PER_PACKAGE = ["/var/log/dpkg.log", "/var/log/dnf.rpm.log"]

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
 *
 * `about` narrows it to one package, on that package's page: the same logs
 * searched for its name, dpkg's per-package record first. The narrowing is a
 * chip the reader can press away, and is kept apart from the list's own Log
 * view so reading one package's history does not leave the list's filtered.
 */
export function PackageLogView({
  product,
  about,
  paneClassName = "h-[min(75vh,40rem)] min-h-80",
}: {
  product?: string
  about?: string
  paneClassName?: string
}) {
  const files = useLogFiles(PACKAGE_LOGS)
  const sources = useMemo(() => {
    if (!files.data) return undefined
    const all = packageLogSources(files.data, product)
    if (!about) return all
    const first = (path?: string) => (path && PER_PACKAGE.includes(path) ? 0 : 1)
    return [...all].sort((a, b) => first(a.path) - first(b.path))
  }, [files.data, product, about])
  const ask = useMemo(() => (about ? { key: about, q: about } : undefined), [about])
  if (files.error && !files.data) {
    return <ErrorState error={files.error} onRetry={files.refresh} />
  }
  if (!sources) return <LoadingPanel />
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
      storageKey={about ? ABOUT_STORAGE_KEY : STORAGE_KEY}
      ask={ask}
      modes={["search", "insights"]}
      // A package log a day long is usually empty: History opens on all the
      // live file holds, a month of apt's transactions, until the reader
      // picks another window.
      initialRange="all"
      pickerLabel="Package log"
      paneClassName={paneClassName}
    />
  )
}

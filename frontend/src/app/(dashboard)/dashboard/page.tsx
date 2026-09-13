"use client"

import { useMemo, useState } from "react"
import { CheckCircle, Clock, ClockRewind, GitBranch, Sparkles } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import type { Release } from "@/lib/types"
import { useSelfUpdate } from "@/hooks/use-self-update"
import { DashboardUpdatePanel } from "@/components/update/update-panel"
import { ReleaseList } from "@/components/update/release-notes"
import { Page, PageHeader, Section, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"

/**
 * The dashboard's own version, and everything it has ever been.
 *
 * This used to be the top half of a page whose bottom half was apt, on the
 * theory that "what can be updated on this machine" is one question. It is
 * not: one of them is the operator's server and the other is the tool they
 * are looking at it through, they are updated by completely different
 * machinery, and the release notes — the part somebody actually reads before
 * upgrading a root-equivalent panel — had nowhere to live but a sheet over
 * the top of a package table.
 *
 * So the history is the page now, and the update control sits above it. The
 * check itself needs no button: opening this page is what asks (see
 * SelfUpdateProvider), which is why "Checked" below is usually seconds old.
 */
export default function DashboardVersionPage() {
  const { report, error, loading } = useSelfUpdate()
  const [filter, setFilter] = useState("")

  // Everything this build knows about, newest first: the releases the check
  // turned up sit above the ones compiled in, and a version in both is shown
  // once. An install with no network still has the whole of its own past.
  const all = useMemo<Release[]>(() => {
    if (!report) return []
    const seen = new Set(report.releases.map((r) => r.version))
    return [...report.releases, ...report.history.filter((r) => !seen.has(r.version))]
  }, [report])

  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    if (!needle) return all
    return all.filter((release) =>
      [release.version, release.title, release.summary ?? "", ...release.changes.map((c) => c.text)]
        .join(" ")
        .toLowerCase()
        .includes(needle),
    )
  }, [all, filter])

  const behind = report?.releases.length ?? 0

  return (
    <Page>
      <PageHeader eyebrow="Operations" title="Dashboard" />

      <StatGrid columns={4}>
        <StatTile
          label="Installed"
          icon={Sparkles}
          value={report ? report.version : "—"}
          hint={report?.install.supported ? "updates in place" : "updates by hand"}
        />
        <StatTile
          label="Status"
          icon={CheckCircle}
          value={report ? (behind === 0 ? "Current" : `${behind} behind`) : "—"}
          tone={behind > 0 ? "warning" : report ? "success" : "default"}
          hint={
            behind > 0
              ? `${report?.latest} is available`
              : report?.check.enabled
                ? "the newest published version"
                : "version checks are off"
          }
        />
        <StatTile
          label="Checked"
          icon={Clock}
          value={
            report?.check.checkedAt
              ? relativeTime(report.check.checkedAt)
              : report?.check.enabled
                ? "never"
                : "off"
          }
          tone={report?.check.error ? "warning" : "default"}
          hint={report?.check.error ? "the last check failed" : "re-checked when this page loads"}
        />
        <StatTile
          label="Tracking"
          icon={GitBranch}
          value={report?.check.ref ?? "—"}
          hint={report?.check.repo}
        />
      </StatGrid>

      {error && <ErrorState error={error} />}
      {loading && !report && (
        <>
          <Skeleton className="h-40 rounded-xl" />
          <LoadingPanel />
        </>
      )}

      {report && <DashboardUpdatePanel />}

      {report && (
        <Section title="Version history">
          <Panel>
            <PanelHeader title="Release notes" />
            <PanelToolbar>
              <SearchInput
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder="Find a change, a version or a fix"
              />
            </PanelToolbar>
            <PanelBody>
              {visible.length === 0 ? (
                <EmptyState
                  icon={ClockRewind}
                  title={filter ? "Nothing matches that" : "No release notes"}
                  description={
                    filter
                      ? "Try a shorter word — the notes are searched by version, title and every line of every change."
                      : "This build carries no changelog, which usually means it was built from a working tree rather than a release."
                  }
                />
              ) : (
                <ReleaseList releases={visible} installed={report.version} />
              )}
            </PanelBody>
          </Panel>
        </Section>
      )}
    </Page>
  )
}

"use client"

import { useMemo, useState } from "react"
import { ClockRewind, Warning } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { Release } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useSelfUpdate } from "@/hooks/use-self-update"
import { useConfirm } from "@/components/confirm-dialog"
import { ReleaseList } from "@/components/update/release-notes"
import { UpdateProgress } from "@/components/update/update-progress"
import { Page, PageHeader, PageState, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
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
 * Laid out as the host Overview is the server: a header whose actions are the
 * two commands, a row of facts about the install, three readings, then the
 * history as a titled list. The update used to be a framed panel of its own
 * with a headline of the newest release inside it — a box repeating the first
 * entry of the list underneath. The button is in the header now, the newest
 * release is the first row of the history, and the block between them is only
 * drawn while an upgrade is running or has just finished. The check itself
 * needs no button: opening this page is what asks (see SelfUpdateProvider),
 * which is why "Checked" is usually seconds old.
 */
export default function DashboardVersionPage() {
  const { can } = useAuth()
  const { report, error, restarting, check, checking, install } = useSelfUpdate()
  const { confirm, dialog } = useConfirm()
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

  if (!report) {
    return (
      <PageState
        eyebrow="System"
        title="Settings"
        error={error}
        skeleton={
          <>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 [&>*]:min-w-0">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-24 rounded-xl" />
              ))}
            </div>
            <Skeleton className="h-48 rounded-xl" />
          </>
        }
      />
    )
  }

  const run = report.run
  const running = run?.status === "running" || run?.status === "pending"
  const target = report.latest
  const behind = report.releases.length
  const canInstall = can("system.admin") && report.install.supported && !running
  const dirty = report.install.dirty ?? []

  const startInstall = () =>
    confirm({
      title: `Update to ${target}`,
      phrase: target,
      confirmLabel: "Update now",
      description: (
        <>
          <p>
            Pulls {report.check.repo} at <b>{report.check.ref}</b>, rebuilds every image in this
            stack and restarts it — the dashboard included.
          </p>
          <p className="text-muted-foreground">
            It runs in its own container so it survives the restart, and takes a few minutes. Your
            data, accounts and settings are untouched.
          </p>
        </>
      ),
      action: async (phrase) => {
        await install(phrase)
      },
    })

  const notices =
    Boolean(report.check.error) ||
    (report.breaking && !running) ||
    (report.available && !running && dirty.length > 0) ||
    !report.install.supported

  return (
    <Page className="animate-rise">
      {dialog}
      <PageHeader
        eyebrow="System"
        title="Settings"
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              pending={checking}
              disabled={!report.check.enabled}
              onClick={() =>
                check().catch((err) => notify.error("Check failed", errorMessage(err)))
              }
            >
              Check now
            </Button>
            {report.available && canInstall && (
              <Button size="sm" onClick={startInstall}>
                Update to {target}
              </Button>
            )}
          </>
        }
      />

      {/* What this install is: the one place the version, the repository it
          follows and the verdict are said in a sentence rather than a figure. */}
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-body text-muted-foreground">
        <span>
          Just Dashboard <span className="numeric text-foreground">{report.version}</span>
        </span>
        <Dot />
        <span className="min-w-0 truncate">
          tracking <span className="font-mono text-foreground">{report.check.repo}</span> at{" "}
          <span className="font-mono text-foreground">{report.check.ref}</span>
        </span>
        <Status
          tone={report.available ? "notice" : report.check.enabled ? "running" : "unknown"}
          label={
            report.available
              ? `${target} available`
              : report.check.enabled
                ? "up to date"
                : "version checks off"
          }
        />
      </div>

      <StatGrid columns={3}>
        <StatTile
          label="Installed"
          value={report.version}
          hint={report.install.supported ? "updates in place" : "updated by hand"}
        />
        <StatTile
          key={target ?? "none"}
          label="Latest"
          value={target ?? report.version}
          tone={behind > 0 ? "warning" : report.check.enabled ? "success" : "default"}
          hint={
            behind > 0
              ? `${behind} ${behind === 1 ? "release" : "releases"} ahead of yours`
              : report.check.enabled
                ? "the newest published version"
                : "version checks are off"
          }
          className="animate-rise"
        />
        <StatTile
          key={report.check.checkedAt ?? "never"}
          label="Checked"
          value={
            report.check.checkedAt
              ? relativeTime(report.check.checkedAt)
              : report.check.enabled
                ? "never"
                : "off"
          }
          tone={report.check.error ? "warning" : "default"}
          hint={report.check.error ? "the last check failed" : "asked again when this page opens"}
          className="animate-rise"
        />
      </StatGrid>

      {error && <ErrorState error={error} />}

      {run && (
        <Panel plain className="animate-rise">
          <PanelHeader title={running ? "Updating" : "Last update"} />
          <PanelBody>
            <UpdateProgress run={run} log={report.log} restarting={restarting} />
          </PanelBody>
        </Panel>
      )}

      {notices && (
        <div className="space-y-3">
          {report.check.error && (
            <Notice title="The last version check failed" tone="warning">
              {report.check.error} — the dashboard keeps running exactly as it is; only the check is
              affected.
            </Notice>
          )}

          {report.breaking && !running && (
            <Notice title="This update needs something done by hand" tone="warning">
              Read the release notes before installing it.
            </Notice>
          )}

          {/* A local edit is a warning rather than a bar: the upgrade
              fast-forwards, so an edited compose file or Caddyfile survives
              unless it genuinely collides — and when it does, git says so and
              nothing is lost. Saying it up front is the difference between an
              upgrade that stops with an explanation and one that surprises. */}
          {report.available && !running && dirty.length > 0 && (
            <Notice title="The install directory has uncommitted changes" icon={Warning}>
              <p>
                {report.install.dir} carries edits that are not in git. The update fast-forwards
                rather than resetting, so they survive unless the new version changes the same lines
                — in which case it stops and tells you, rather than discarding them.
              </p>
              <ul className="mt-1 space-y-0.5 font-mono text-hint">
                {dirty.slice(0, 6).map((line) => (
                  <li key={line}>{line}</li>
                ))}
              </ul>
            </Notice>
          )}

          {!report.install.supported && (
            <Notice title="This install updates by hand">
              {report.install.reason}. Everything else on this page still works; only the one-click
              update does not.
            </Notice>
          )}
        </div>
      )}

      <Panel plain>
        <PanelHeader
          title="Version history"
          actions={
            <SearchInput
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Find a change, a version or a fix"
            />
          }
        />
        <PanelBody>
          {visible.length > 0 ? (
            <ReleaseList releases={visible} installed={report.version} />
          ) : filter ? (
            <EmptyNote>
              Nothing matches that. The notes are searched by version, title and every line of
              every change.
            </EmptyNote>
          ) : (
            <EmptyState
              icon={ClockRewind}
              title="No release notes"
              description="This build carries no changelog, which usually means it was built from a working tree rather than a release."
            />
          )}
        </PanelBody>
      </Panel>
    </Page>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}

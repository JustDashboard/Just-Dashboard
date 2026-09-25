"use client"

import { useMemo } from "react"
import { useSessionState } from "@/lib/view-state"
import { ArrowCircleUp, ClockRewind, External, RefreshClockwise, Warning } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { Release, SelfUpdateReport } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useSelfUpdate } from "@/hooks/use-self-update"
import { useConfirm } from "@/components/confirm-dialog"
import { LogoGlyph } from "@/components/logo"
import { ProductGlyph } from "@/components/product-logo"
import { ReleaseTimeline } from "@/components/update/release-timeline"
import { UpdateProgress } from "@/components/update/update-progress"
import { Page, PageContext, PageState, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

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
 * The page is its history. The two commands sit with the install's identity,
 * then the update while one is running and the
 * releases as a timeline. The check itself needs no button to happen: opening
 * this page is what asks (see SelfUpdateProvider), which is why "checked" is
 * usually seconds old.
 *
 * **Three readings used to sit between the header and the history** —
 * Installed, Latest and Checked — and §15 pass 2 is dropped here the way
 * `/git` drops it, by naming where each went. *Installed* is the figure in the
 * identity line, beside the mark it is the version of, and the filled mark on
 * the timeline. *Latest* is the identity line's status ("0.7.0 available",
 * with how many releases ahead) and the first entry of the history, tagged.
 * *Checked* is the hint under that status, and a failed check turns the
 * status itself amber. What the tiles did beyond saying those numbers — put
 * "there is an update" first — the brand-faced button in the page controls does, and
 * did already.
 */
export default function DashboardVersionPage() {
  const { can } = useAuth()
  const { report, error, restarting, check, checking, install } = useSelfUpdate()
  const { confirm, dialog } = useConfirm()
  const [filter, setFilter] = useSessionState("dashboard.releases.query", "")

  // Everything this build knows about, newest first: the releases the check
  // turned up sit above the ones compiled in, and a version in both is shown
  // once. An install with no network still has the whole of its own past.
  const all = useMemo<Release[]>(() => {
    if (!report) return []
    const seen = new Set(report.releases.map((r) => r.version))
    return [...report.releases, ...report.history.filter((r) => !seen.has(r.version))]
  }, [report])
  const newer = useMemo(() => new Set(report?.releases.map((r) => r.version)), [report])

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
        eyebrow="Settings"
        title="Version"
        error={error}
        skeleton={
          <>
            <Skeleton className="h-14 rounded-xl" />
            <Skeleton className="h-96 rounded-xl" />
          </>
        }
      />
    )
  }

  const run = report.run
  const running = run?.status === "running" || run?.status === "pending"
  const target = report.latest
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
    (report.breaking && !running) || (report.available && !running && dirty.length > 0)

  return (
    <Page className="animate-rise">
      {dialog}
      <PageContext eyebrow="Settings" title="Version" />

      <Identity
        report={report}
        actions={
          <>
            <Button
              variant="outline"
              pending={checking}
              disabled={!report.check.enabled}
              onClick={() =>
                check().catch((err) => notify.error("Check failed", errorMessage(err)))
              }
            >
              {!checking && <RefreshClockwise />}
              Check now
            </Button>
            {report.available && canInstall && (
              <Button onClick={startInstall}>
                <ArrowCircleUp />
                Update to {target}
              </Button>
            )}
          </>
        }
      />

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
        <PanelBody className="pt-8">
          {visible.length > 0 ? (
            <ReleaseTimeline
              releases={visible}
              installed={report.version}
              newer={newer}
              expanded={Boolean(filter.trim())}
            />
          ) : filter ? (
            <EmptyNote>
              Nothing matches that. The notes are searched by version, title and every line of every
              change.
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

/**
 * What this install is, in one line: the mark and the version it is running,
 * the repository it follows, and what the last check said about it.
 *
 * The repository is drawn as itself — GitHub's mark, linking to the branch —
 * because it is the one place outside this machine the dashboard reaches, and
 * a reader deciding whether to trust an update wants to know where from.
 */
function Identity({ report, actions }: { report: SelfUpdateReport; actions: React.ReactNode }) {
  const behind = report.releases.length
  const status = report.available
    ? { tone: "notice" as const, label: `${report.latest} available` }
    : report.check.error
      ? { tone: "warning" as const, label: "the last check failed" }
      : report.check.enabled
        ? { tone: "running" as const, label: "Up to date" }
        : { tone: "unknown" as const, label: "Version checks off" }
  const checked = report.check.checkedAt ? `checked ${relativeTime(report.check.checkedAt)}` : ""
  const hint = report.available
    ? `${behind} ${behind === 1 ? "release" : "releases"} ahead of yours`
    : report.check.enabled
      ? [
          checked,
          report.latest && report.latest !== report.version && `newest published ${report.latest}`,
        ]
          .filter(Boolean)
          .join(" · ")
      : "turn them on in Configuration"

  return (
    <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-10 gap-y-4 border-b border-hairline pb-6">
      <div className="flex min-w-0 items-center gap-4">
        <span
          aria-hidden
          className="flex size-12 shrink-0 items-center justify-center rounded-xl border border-hairline bg-surface-sunken"
        >
          <LogoGlyph className="h-6 w-auto text-brand" />
        </span>
        <div className="min-w-0 space-y-1">
          <p className="flex min-w-0 items-baseline gap-2">
            <span className="text-title font-semibold tracking-tight">Just Dashboard</span>
            <span className="numeric text-2xl leading-none font-semibold tracking-tight">
              {report.version}
            </span>
          </p>
          <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            <a
              href={`https://github.com/${report.check.repo}/tree/${report.check.ref}`}
              target="_blank"
              rel="noreferrer"
              className="inline-flex min-w-0 items-center gap-1.5 rounded-sm focus-ring transition-colors hover:text-foreground"
            >
              <ProductGlyph id="github" />
              <span className="truncate font-mono text-foreground">{report.check.repo}</span>
              <External aria-hidden className="size-3 shrink-0" />
            </a>
            <span className="text-muted-foreground/40">·</span>
            <span>
              tracking <span className="font-mono text-foreground">{report.check.ref}</span>
            </span>
            <span className="text-muted-foreground/40">·</span>
            {report.install.supported ? (
              <span>updates in place</span>
            ) : (
              <Tooltip>
                <TooltipTrigger asChild>
                  <span
                    tabIndex={0}
                    className="cursor-help rounded-sm underline decoration-dotted underline-offset-2 focus-ring"
                  >
                    updated by hand
                  </span>
                </TooltipTrigger>
                <TooltipContent className="max-w-xs">
                  {report.install.reason}. Everything else on this page still works; only the
                  one-click update does not.
                </TooltipContent>
              </Tooltip>
            )}
          </p>
        </div>
      </div>

      <div className="min-w-0 space-y-2 sm:text-right">
        {report.check.error ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <span tabIndex={0} className="inline-flex cursor-help rounded-sm focus-ring">
                <Status tone={status.tone} label={status.label} className="text-body font-medium" />
              </span>
            </TooltipTrigger>
            <TooltipContent className="max-w-xs">
              {report.check.error} — the dashboard keeps running exactly as it is; only the check is
              affected.
            </TooltipContent>
          </Tooltip>
        ) : (
          <Status tone={status.tone} label={status.label} className="text-body font-medium" />
        )}
        {hint && <p className="text-hint text-muted-foreground">{hint}</p>}
        <div className="flex flex-wrap items-center gap-2 sm:justify-end">{actions}</div>
      </div>
    </div>
  )
}

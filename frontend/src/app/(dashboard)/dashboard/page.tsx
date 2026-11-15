"use client"

import { useMemo } from "react"
import { useSessionState } from "@/lib/view-state"
import { ArrowCircleUp, ClockRewind, External, RefreshClockwise } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { Release, SelfUpdateReport } from "@/lib/types"
import { offerTagNote, pendingNote } from "@/lib/update-offer"
import { useAuth } from "@/hooks/use-auth"
import { useSelfUpdate } from "@/hooks/use-self-update"
import { useUpdateHistory } from "@/hooks/use-update-history"
import { LogoGlyph } from "@/components/logo"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { ProductGlyph } from "@/components/product-logo"
import { NoticeLine, NoticeLines } from "@/components/settings-column"
import { ReleaseTimeline } from "@/components/update/release-timeline"
import { useUpdateDialog } from "@/components/update/update-dialog"
import { UpdateProgress } from "@/components/update/update-progress"
import { Page, PageContext, PageState, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
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
 * the timeline. *Latest* is the line's state beside the version ("0.7.0
 * available", with how many releases ahead as the last of its facts) and the
 * first entry of the history, tagged. *Checked* is those same last words, and
 * a failed check turns the state itself amber. The same words say whether what is
 * offered is a tagged release, and name a version the branch carries that has
 * no tag yet, which is never offered. What the tiles did beyond saying those
 * numbers — put "there is an update" first — the brand-faced command at the
 * line's far end does, and did already.
 *
 * **What an update needs said before it is installed is a line each**, under
 * the identity line: that it wants something done by hand, with the way to
 * the release's own note, and that the checkout carries edits, with the files
 * a press away. They were two framed notices with a paragraph in each, which
 * stood between the header and the history on every visit until the update
 * was installed.
 */
export default function DashboardVersionPage() {
  const { can } = useAuth()
  const { report, error, restarting, check, checking } = useSelfUpdate()
  const { openUpdate, dialog } = useUpdateDialog()
  const [filter, setFilter] = useSessionState("dashboard.releases.query", "")

  // Everything this build knows about, newest first: the releases the check
  // turned up sit above the ones compiled in, and a version in both is shown
  // once. An install with no network still has the whole of its own past.
  // The compiled-in ones are read apart from the report, once: they change only with the build.
  const history = useUpdateHistory(report?.version)
  const all = useMemo<Release[]>(() => {
    if (!report) return []
    const seen = new Set(report.releases.map((r) => r.version))
    return [...report.releases, ...(history ?? []).filter((r) => !seen.has(r.version))]
  }, [report, history])
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

  const breaking = report.breaking && !running
  const edited = report.available && !running && dirty.length > 0
  // The release whose note says what has to be done by hand.
  const noted = report.releases.find((release) => release.breaking)?.version ?? target

  return (
    <Page className="animate-rise">
      {dialog}
      <PageContext eyebrow="Settings" title="Version" />

      <Identity
        report={report}
        command={
          <>
            <Button
              size="sm"
              variant="outline"
              pending={checking}
              disabled={!report.check.enabled}
              onClick={() =>
                check().catch((err) => notify.error("Check failed", errorMessage(err)))
              }
            >
              {!checking && <RefreshClockwise />}
              {/* A glyph on a phone, where the line's first row also holds the name. */}
              <span className="max-sm:sr-only">Check now</span>
            </Button>
            {report.available && canInstall && (
              <Button size="sm" onClick={openUpdate}>
                <ArrowCircleUp />
                <span>
                  Update<span className="max-sm:sr-only"> to {target}</span>
                </span>
              </Button>
            )}
          </>
        }
      />

      {(breaking || edited) && (
        <NoticeLines>
          {breaking && (
            <NoticeLine
              tone="warning"
              title={`${noted ?? "This update"} needs something done by hand`}
            >
              <Button
                size="xs"
                variant="outline"
                onClick={() => {
                  // A search may have filtered the release out of the history.
                  setFilter("")
                  window.requestAnimationFrame(() =>
                    document
                      .getElementById(`release-${noted}`)
                      ?.scrollIntoView({ block: "start", behavior: "smooth" }),
                  )
                }}
              >
                Read the notes
              </Button>
            </NoticeLine>
          )}

          {/* A local edit is a line rather than a bar: the upgrade
              fast-forwards, so an edited compose file or Caddyfile survives
              unless it genuinely collides — and when it does, git says so and
              nothing is lost. Saying it up front is the difference between an
              upgrade that stops with an explanation and one that surprises. */}
          {edited && (
            <NoticeLine
              tone="notice"
              title={plural(dirty.length, "uncommitted change")}
              detail={
                <>
                  in <span className="font-mono">{report.install.dir}</span> · kept by the update
                </>
              }
            >
              <Popover>
                <PopoverTrigger asChild>
                  <Button size="xs" variant="outline">
                    Show
                  </Button>
                </PopoverTrigger>
                <PopoverContent align="end" className="w-80 space-y-2 text-xs">
                  <ul className="space-y-0.5 font-mono text-hint">
                    {dirty.slice(0, 6).map((line) => (
                      <li key={line} className="truncate">
                        {line}
                      </li>
                    ))}
                    {dirty.length > 6 && (
                      <li className="font-sans text-muted-foreground">
                        and {dirty.length - 6} more
                      </li>
                    )}
                  </ul>
                  <p className="leading-relaxed text-muted-foreground">
                    The update fast-forwards rather than resetting, so these survive unless the new
                    version changes the same lines. Then it stops and says so, and discards nothing.
                  </p>
                </PopoverContent>
              </Popover>
            </NoticeLine>
          )}
        </NoticeLines>
      )}

      {error && <ErrorState error={error} />}

      {run && (
        <Panel plain className="animate-rise">
          <PanelHeader title={running ? "Updating" : "Last update"} />
          <PanelBody>
            <UpdateProgress run={run} log={report.log} restarting={restarting} />
          </PanelBody>
        </Panel>
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
          ) : history === undefined ? (
            <Skeleton className="h-96 rounded-xl" />
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
function Identity({ report, command }: { report: SelfUpdateReport; command: React.ReactNode }) {
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
    ? [
        `${behind} ${behind === 1 ? "release" : "releases"} ahead of yours`,
        offerTagNote(report),
        pendingNote(report),
      ]
        .filter(Boolean)
        .join(" · ")
    : report.check.enabled
      ? [
          checked,
          report.latest && report.latest !== report.version && `newest published ${report.latest}`,
          pendingNote(report),
        ]
          .filter(Boolean)
          .join(" · ")
      : "turn them on in Configuration"

  return (
    <HostIdentity
      logo={
        <span
          aria-hidden
          className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-hairline bg-surface-sunken"
        >
          <LogoGlyph className="h-5 w-auto text-brand" />
        </span>
      }
      title={
        <span className="flex min-w-0 items-baseline gap-2">
          <span className="truncate">Just Dashboard</span>
          <span className="numeric shrink-0">{report.version}</span>
          {report.commit && (
            <span
              className="shrink-0 font-mono text-xs font-normal text-muted-foreground"
              title={`Built from commit ${report.commit}, which is not the ${report.version} release`}
            >
              +{report.commit}
            </span>
          )}
        </span>
      }
      // What the last check found stands beside the version, as a project's
      // state stands beside its name, and its words are the last of the facts:
      // stacked over the commands they made the line three rows tall.
      state={
        report.check.error ? (
          <Tooltip>
            <TooltipTrigger asChild>
              {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- takes the keyboard so that its tooltip can be read without a pointer */}
              <span tabIndex={0} className="inline-flex cursor-help rounded-sm focus-ring">
                <Status tone={status.tone} label={status.label} />
              </span>
            </TooltipTrigger>
            <TooltipContent className="max-w-xs">
              {report.check.error} — the dashboard keeps running exactly as it is; only the check is
              affected.
            </TooltipContent>
          </Tooltip>
        ) : (
          <Status tone={status.tone} label={status.label} />
        )
      }
      facts={
        <>
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
          <FactDot />
          <span>
            tracking <span className="font-mono text-foreground">{report.check.ref}</span>
          </span>
          <FactDot />
          {report.install.supported ? (
            <span>updates in place</span>
          ) : (
            <Tooltip>
              <TooltipTrigger asChild>
                <span
                  // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- takes the keyboard so that its tooltip can be read without a pointer
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
          {hint && (
            <>
              <FactDot />
              <span>{hint}</span>
            </>
          )}
        </>
      }
      command={command}
    />
  )
}

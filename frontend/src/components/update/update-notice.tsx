"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { ArrowCircleUp, ArrowRight, CrossCircle, Sparkles } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { phaseLabel, updateHasNews, useSelfUpdate } from "@/hooks/use-self-update"
import { useUpdateHistory } from "@/hooks/use-update-history"
import { LogoGlyph } from "@/components/logo"
import { Segment } from "@/components/deploy/run-pipeline"
import { KindBand, UpdateWire } from "@/components/update/update-art"
import { useUpdateDialog } from "@/components/update/update-dialog"
import { updatePhases } from "@/components/update/update-progress"
import { RAIL_NOTICE_LINK, RailNotice } from "@/components/rail-notice"
import { BlurFade } from "@/components/ui/blur-fade"
import { Button } from "@/components/ui/button"
import { Confetti, type ConfettiRef } from "@/components/ui/confetti"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Spinner } from "@/components/loading"

/**
 * "There is a newer version", where the operator will see it.
 *
 * Above the account card at the foot of the rail, because that is the one
 * region of the shell that is on every page and is not a place you can
 * navigate to — the same argument the account card itself makes. A page for
 * this would be a page nobody visits, which is exactly how self-hosted panels
 * end up running eighteen months behind.
 *
 * It renders **nothing at all** when there is nothing to say. A permanent
 * "you're up to date" row is the sort of chrome that gets tuned out, and it
 * would take the tuning-out with it on the day the row says something else.
 * The version an operator wants to know casually is already beside the
 * wordmark at the top of the same rail — or, on a server linked to a Just
 * Hub, on this server's row of the switcher that takes the wordmark's place.
 *
 * When it does speak it is the account card's container — the same edge and
 * ground as the card under it, tinted green or red only once a run has
 * finished — and the life is in what it draws, not behind it: the installed
 * version wired to the newest with a pulse running toward it, and a band of
 * everything the releases between them bring, in the kinds' own colours.
 * While it installs, the beam runs round the card and the stage is lit; when
 * an install watched here succeeds, paper. The washes of the sign-in screens
 * were tried here and read as out of place in the shell.
 *
 * The notes are the Version page's (`/dashboard`), not a panel of their own:
 * that page already is the history, searchable, with the update in its
 * header, and a second rendering of it over whatever the operator was doing
 * was a second place for the two to disagree.
 */
export function UpdateNotice({ collapsed }: { collapsed: boolean }) {
  const { can } = useAuth()
  const { report, restarting, dismiss, outcome } = useSelfUpdate()
  const { openUpdate, dialog } = useUpdateDialog()
  const confetti = useRef<ConfettiRef>(null)
  // Only an outcome is headed by a release that may no longer be in the report: the notes of
  // the version just installed are in the new build's own history.
  const history = useUpdateHistory(report?.version, Boolean(outcome))

  const run = report?.run
  const running = run?.status === "running" || run?.status === "pending"

  // An update that finishes while somebody is watching it is §11's *once*.
  // Arriving at a page after it already succeeded is not the moment.
  const status = run?.status
  const wasRunning = useRef(running)
  useEffect(() => {
    if (wasRunning.current && !running && status === "success") confetti.current?.fire()
    wasRunning.current = running
  }, [running, status])

  // Nothing to say: no update, nothing running, and no outcome left unread.
  if (!updateHasNews(report, outcome)) return null

  const mode = running
    ? "running"
    : outcome?.status === "failed"
      ? "failed"
      : outcome?.status === "success"
        ? "success"
        : "available"
  const tone = mode === "failed" ? "danger" : mode === "success" ? "success" : "brand"
  const target = report.latest
  const canInstall = can("system.admin") && report.install.supported && !running

  const label =
    mode === "running"
      ? restarting
        ? "Restarting the dashboard"
        : phaseLabel(run!)
      : mode === "failed"
        ? `Update to ${outcome!.toVersion} failed`
        : mode === "success"
          ? `Updated to ${outcome!.toVersion}`
          : `Version ${target} is available`

  const celebrate = (
    <Confetti ref={confetti} className="pointer-events-none fixed inset-0 z-overlay size-full" />
  )

  const glyph =
    mode === "running" ? (
      <Spinner className="size-4" />
    ) : mode === "failed" ? (
      <CrossCircle className="size-4" />
    ) : mode === "success" ? (
      <Sparkles className="size-4" />
    ) : (
      <ArrowCircleUp className="size-4" />
    )

  if (collapsed) {
    return (
      <>
        {celebrate}
        <RailNotice
          tone={tone}
          collapsed
          label={label}
          href="/dashboard"
          glyph={glyph}
          section="Dashboard update"
          beam={mode === "running"}
        />
      </>
    )
  }

  const headline =
    mode === "success" || mode === "failed"
      ? [...report.releases, ...(history ?? [])].find((r) => r.version === outcome!.toVersion)
      : report.releases[0]
  const delivered = mode === "available" ? report.releases : headline ? [headline] : []

  return (
    <>
      {celebrate}
      <RailNotice
        tone={tone}
        collapsed={false}
        label={label}
        href="/dashboard"
        glyph={glyph}
        section="Dashboard update"
        beam={mode === "running"}
        // Keyed on the mode so a notice that turns from "available" into
        // "updating" into "updated" arrives again each time it says
        // something new, rather than swapping its words in place.
        contentKey={mode}
        mark={<Mark mode={mode} />}
        // Dismissal is for a run that has finished and been read. An update
        // that is merely available has nothing to dismiss — it is still
        // available tomorrow, and a notice that can be silenced is one an
        // install is silently three versions behind on.
        onDismiss={
          mode === "success" || mode === "failed" ? () => dismiss().catch(() => {}) : undefined
        }
        head={
          <BlurFade className="space-y-1.5">
            <p
              className={cn(
                "eyebrow leading-none",
                tone === "brand" && "text-signal",
                tone === "success" && "text-success",
                tone === "danger" && "text-destructive",
              )}
            >
              {mode === "running"
                ? "Updating"
                : mode === "failed"
                  ? "Update failed"
                  : mode === "success"
                    ? "Updated"
                    : "New version"}
            </p>
            <p className="flex min-w-0 items-center gap-2">
              <span className="numeric text-xs text-muted-foreground">
                {run && mode !== "available" ? run.fromVersion : report.version}
              </span>
              <UpdateWire tone={tone} still={mode === "success" || mode === "failed"} />
              <span className="numeric text-sm leading-tight font-semibold">
                {run && mode !== "available" ? run.toVersion : target}
              </span>
            </p>
          </BlurFade>
        }
      >
        <BlurFade delay={0.06} className="space-y-2.5">
          {mode === "running" ? (
            <>
              <p className="truncate text-xs font-medium">
                <TextShimmer>{label}</TextShimmer>
              </p>
              <span aria-hidden className="grid grid-cols-3 gap-1">
                {updatePhases(run!, report.log).map((phase) => (
                  <Segment key={phase.label} state={phase.state} />
                ))}
              </span>
              <p className="text-hint leading-snug text-muted-foreground">
                Keep this tab open — it picks back up after the restart.
              </p>
            </>
          ) : mode === "failed" ? (
            <p className="line-clamp-3 font-mono text-hint leading-snug break-words text-destructive">
              {run?.error || "The updater stopped before the new version answered."}
            </p>
          ) : (
            headline && (
              <p className="line-clamp-2 text-xs leading-snug font-medium">{headline.title}</p>
            )
          )}
          {mode !== "running" && mode !== "failed" && <KindBand releases={delivered} />}
        </BlurFade>

        <BlurFade delay={0.12} className="flex flex-col gap-2">
          {mode === "available" && canInstall && (
            <Button size="sm" className="w-full" onClick={openUpdate}>
              <ArrowCircleUp />
              Update now
            </Button>
          )}
          {/* An update that exists but cannot be installed from here still
                gets said out loud, with the reason: a dashboard running from
                a binary or an unrecognised directory is a fine install, and
                hiding the notice would leave its operator never finding out. */}
          {mode === "available" && !report.install.supported && (
            <p className="text-hint leading-snug text-muted-foreground">
              This install updates by hand — {report.install.reason}
            </p>
          )}
          <Link href="/dashboard" className={RAIL_NOTICE_LINK}>
            {mode === "running"
              ? "View progress"
              : mode === "failed"
                ? "See what went wrong"
                : mode === "success"
                  ? "See what's new"
                  : "View patch notes"}
            <ArrowRight aria-hidden />
          </Link>
        </BlurFade>
      </RailNotice>
      {dialog}
    </>
  )
}

/** The notice's mark: the dashboard's own J while it is on its way, the outcome once it lands. */
function Mark({ mode }: { mode: "available" | "running" | "success" | "failed" }) {
  if (mode === "success") return <Sparkles />
  if (mode === "failed") return <CrossCircle />
  return <LogoGlyph small />
}

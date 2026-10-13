"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { ArrowCircleUp, ArrowRight, Cross, CrossCircle, Sparkles } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { phaseLabel, useSelfUpdate } from "@/hooks/use-self-update"
import { LogoGlyph } from "@/components/logo"
import { Segment } from "@/components/deploy/run-pipeline"
import { KindBand, UpdateWire } from "@/components/update/update-art"
import { useUpdateDialog } from "@/components/update/update-dialog"
import { updatePhases } from "@/components/update/update-progress"
import { BlurFade } from "@/components/ui/blur-fade"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { Confetti, type ConfettiRef } from "@/components/ui/confetti"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Spinner } from "@/components/state"

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
 * wordmark at the top of the same rail.
 *
 * When it does speak it is the liveliest thing in the shell, on purpose. It
 * stands on arrival's light (`.update-art`, design system §16) — the one
 * element of the shell that does, because it is the one that announces
 * something arriving — and it draws the update as what it is: the installed
 * version wired to the newest with a pulse running toward it, and a band of
 * everything the releases between them bring, in the kinds' own colours.
 * While it installs, the beam runs round the card and the stage is lit; when
 * an install watched here succeeds, paper.
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

  const run = report?.run
  const running = run?.status === "running" || run?.status === "pending"
  const available = Boolean(report?.available)

  // An update that finishes while somebody is watching it is §11's *once*.
  // Arriving at a page after it already succeeded is not the moment.
  const status = run?.status
  const wasRunning = useRef(running)
  useEffect(() => {
    if (wasRunning.current && !running && status === "success") confetti.current?.fire()
    wasRunning.current = running
  }, [running, status])

  // Nothing to say: no update, nothing running, and no outcome left unread.
  if (!report || (!available && !running && !outcome)) return null

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
    <Confetti ref={confetti} className="pointer-events-none fixed inset-0 z-50 size-full" />
  )

  if (collapsed) {
    return (
      <>
        {celebrate}
        <Tooltip>
          <TooltipTrigger asChild>
            <Link
              href="/dashboard"
              aria-label={label}
              data-tone={tone}
              className={cn(
                "update-art relative flex size-8 items-center justify-center rounded-lg border focus-ring transition-colors",
                tone === "brand" && "border-rule-brand text-brand hover:bg-sidebar-accent",
                tone === "success" && "border-rule-success text-success",
                tone === "danger" && "border-rule-danger text-destructive",
              )}
            >
              {mode === "running" && <BorderBeam size={24} duration={2.5} />}
              {mode === "running" ? (
                <Spinner className="size-4" />
              ) : mode === "failed" ? (
                <CrossCircle className="size-4" />
              ) : mode === "success" ? (
                <Sparkles className="size-4" />
              ) : (
                <ArrowCircleUp className="size-4" />
              )}
            </Link>
          </TooltipTrigger>
          <TooltipContent side="right">{label}</TooltipContent>
        </Tooltip>
      </>
    )
  }

  const headline =
    mode === "success" || mode === "failed"
      ? [...report.releases, ...report.history].find((r) => r.version === outcome!.toVersion)
      : report.releases[0]
  const delivered = mode === "available" ? report.releases : headline ? [headline] : []

  return (
    <>
      {celebrate}
      <section
        aria-label="Dashboard update"
        data-tone={tone}
        className={cn(
          "update-art relative isolate min-w-0 animate-rise overflow-hidden rounded-xl border bg-card",
          tone === "brand" && "border-rule-brand",
          tone === "success" && "border-rule-success",
          tone === "danger" && "border-rule-danger",
        )}
      >
        {mode === "running" && <BorderBeam size={72} duration={4} />}

        {/* Keyed on the mode so a notice that turns from "available" into
            "updating" into "updated" arrives again each time it says
            something new, rather than swapping its words in place. */}
        <div key={mode} className="space-y-3 p-3">
          <BlurFade className="flex min-w-0 items-center gap-2.5">
            <Mark mode={mode} />
            <div className="min-w-0 flex-1 space-y-1">
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
            </div>
            {/* Dismissal is for a run that has finished and been read. An
                update that is merely available has nothing to dismiss — it
                is still available tomorrow, and a notice that can be silenced
                is one an install is silently three versions behind on. */}
            {(mode === "success" || mode === "failed") && (
              <button
                type="button"
                aria-label="Dismiss"
                onClick={() => dismiss().catch(() => {})}
                className="-mr-1 shrink-0 self-start rounded-sm p-0.5 text-muted-foreground focus-ring transition-colors hover:text-foreground"
              >
                <Cross className="size-3" />
              </button>
            )}
          </BlurFade>

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

          <BlurFade delay={0.12} className="flex flex-col gap-1">
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
              <p className="pb-1 text-hint leading-snug text-muted-foreground">
                This install updates by hand — {report.install.reason}
              </p>
            )}
            <Button asChild size="sm" variant="ghost" className="w-full justify-between">
              <Link href="/dashboard">
                {mode === "running"
                  ? "View progress"
                  : mode === "failed"
                    ? "See what went wrong"
                    : mode === "success"
                      ? "See what's new"
                      : "View patch notes"}
                <ArrowRight />
              </Link>
            </Button>
          </BlurFade>
        </div>
      </section>
      {dialog}
    </>
  )
}

/** The notice's mark: the dashboard's own J while it is on its way, the outcome once it lands. */
function Mark({ mode }: { mode: "available" | "running" | "success" | "failed" }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex size-8 shrink-0 items-center justify-center rounded-lg border",
        (mode === "available" || mode === "running") &&
          "border-rule-brand bg-wash-brand text-brand",
        mode === "success" && "border-rule-success bg-wash-success text-success",
        mode === "failed" && "border-rule-danger bg-wash-danger text-destructive",
      )}
    >
      {mode === "success" ? (
        <Sparkles className="size-4" />
      ) : mode === "failed" ? (
        <CrossCircle className="size-4" />
      ) : (
        <LogoGlyph className="h-4 w-auto" />
      )}
    </span>
  )
}

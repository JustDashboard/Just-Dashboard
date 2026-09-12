"use client"

import { useState } from "react"
import { ArrowCircleUp, CheckCircle, Cross, CrossCircle, Sparkles } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"
import { useAuth } from "@/hooks/use-auth"
import { phaseLabel, useSelfUpdate } from "@/hooks/use-self-update"
import { useConfirm } from "@/components/confirm-dialog"
import { ChangesSheet } from "@/components/update/changes-sheet"
import { Button } from "@/components/ui/button"
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
 */
export function UpdateNotice({ collapsed }: { collapsed: boolean }) {
  const { can } = useAuth()
  const { report, restarting, install, dismiss, outcome } = useSelfUpdate()
  const { confirm, dialog } = useConfirm()
  const [sheet, setSheet] = useState(false)

  const run = report?.run
  const running = run?.status === "running" || run?.status === "pending"
  const available = Boolean(report?.available)

  // Nothing to say: no update, nothing running, and no outcome left unread.
  if (!report || (!available && !running && !outcome)) return null

  const target = report.latest
  const headline = report.releases[0]
  const canInstall = can("system.admin") && report.install.supported && !running

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

  // The shared union from components/tone.ts, not a fifth local vocabulary.
  // An update in flight or waiting is a *notice*, which this palette draws in
  // the brand hue rather than in the ink reserved for commands.
  const tone: "brand" | Extract<Tone, "success" | "danger"> = running
    ? "brand"
    : outcome?.status === "failed"
      ? "danger"
      : outcome?.status === "success"
        ? "success"
        : "brand"

  if (collapsed) {
    return (
      <>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              onClick={() => setSheet(true)}
              aria-label={running ? "Update in progress" : `Version ${target} is available`}
              className={cn(
                "relative flex size-8 items-center justify-center rounded-lg focus-ring transition-colors",
                tone === "brand" && "bg-plot-brand text-brand hover:bg-plot-brand-hover",
                tone === "success" && "bg-plot-success text-success hover:bg-plot-success-hover",
                tone === "danger" && "bg-plot-danger text-destructive hover:bg-plot-danger-hover",
              )}
            >
              {running ? (
                <Spinner className="size-4" />
              ) : outcome?.status === "failed" ? (
                <CrossCircle className="size-4" />
              ) : outcome?.status === "success" ? (
                <Sparkles className="size-4" />
              ) : (
                <ArrowCircleUp className="size-4" />
              )}
            </button>
          </TooltipTrigger>
          <TooltipContent side="right">
            {running
              ? phaseLabel(run)
              : outcome?.status === "failed"
                ? "The last update failed"
                : outcome?.status === "success"
                  ? `Updated to ${outcome.toVersion}`
                  : `${target} is available`}
          </TooltipContent>
        </Tooltip>
        <ChangesSheet open={sheet} onOpenChange={setSheet} />
        {dialog}
      </>
    )
  }

  return (
    <>
      <div
        className={cn(
          "min-w-0 space-y-2 rounded-lg border p-2.5",
          tone === "brand" && "border-rule-brand bg-wash-brand",
          tone === "success" && "border-rule-success bg-wash-success",
          tone === "danger" && "border-rule-danger bg-wash-danger",
        )}
      >
        <div className="flex min-w-0 items-start gap-2">
          <span
            className={cn(
              "pt-px",
              tone === "brand" && "text-brand",
              tone === "success" && "text-success",
              tone === "danger" && "text-destructive",
            )}
          >
            {running ? (
              <Spinner className="size-3.5" />
            ) : outcome?.status === "failed" ? (
              <CrossCircle className="size-3.5" />
            ) : outcome?.status === "success" ? (
              <CheckCircle className="size-3.5" />
            ) : (
              <ArrowCircleUp className="size-3.5" />
            )}
          </span>
          <div className="min-w-0 flex-1">
            {running ? (
              <>
                <p className="truncate text-xs leading-tight font-medium">
                  {restarting ? "Restarting" : phaseLabel(run)}
                </p>
                <p className="numeric truncate text-hint leading-tight text-muted-foreground">
                  {run.fromVersion} → {run.toVersion}
                </p>
              </>
            ) : outcome ? (
              <>
                <p className="truncate text-xs leading-tight font-medium">
                  {outcome.status === "success"
                    ? `Updated to ${outcome.toVersion}`
                    : "Update failed"}
                </p>
                <p className="truncate text-hint leading-tight text-muted-foreground">
                  {outcome.status === "success" ? "See what changed" : "See what went wrong"}
                </p>
              </>
            ) : (
              <>
                <p className="truncate text-xs leading-tight font-medium">
                  Version <span className="numeric">{target}</span> is out
                </p>
                <p className="truncate text-hint leading-tight text-muted-foreground">
                  {headline?.title ?? "A new release is available"}
                </p>
              </>
            )}
          </div>
          {/* Dismissal is for a run that has finished and been read. An update
              that is merely available has nothing to dismiss — it is still
              available tomorrow, and a notice that can be silenced is one an
              install is silently three versions behind on. */}
          {outcome && !running && (
            <button
              type="button"
              aria-label="Dismiss"
              onClick={() => dismiss().catch(() => {})}
              className="-mt-0.5 -mr-0.5 shrink-0 rounded-sm p-0.5 text-muted-foreground focus-ring transition-colors hover:text-foreground"
            >
              <Cross className="size-3" />
            </button>
          )}
        </div>

        <div className="flex flex-col gap-1.5">
          {available && !running && !outcome && canInstall && (
            <Button size="xs" className="w-full" onClick={startInstall}>
              Update now
            </Button>
          )}
          <Button
            size="xs"
            variant={available && canInstall && !running && !outcome ? "ghost" : "outline"}
            className="w-full"
            onClick={() => setSheet(true)}
          >
            {running ? "View progress" : outcome ? "Open" : "View changes"}
          </Button>
        </div>

        {/* An update that exists but cannot be installed from here still gets
            said out loud, with the reason: a dashboard running from a binary
            or an unrecognised directory is a fine install, and hiding the
            notice would leave its operator never finding out at all. */}
        {available && !running && !outcome && !report.install.supported && (
          <p className="text-hint leading-relaxed text-muted-foreground">
            This install updates by hand — {report.install.reason}
          </p>
        )}
      </div>
      <ChangesSheet open={sheet} onOpenChange={setSheet} />
      {dialog}
    </>
  )
}

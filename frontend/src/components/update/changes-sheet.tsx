"use client"

import { useMemo, useState } from "react"
import {
  ArrowCircleUp,
  ChevronDown,
  ClockRewind,
  Puzzle,
  RefreshClockwise,
} from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { useSelfUpdate } from "@/hooks/use-self-update"
import { useConfirm } from "@/components/confirm-dialog"
import { ReleaseList } from "@/components/update/release-notes"
import { UpdateProgress } from "@/components/update/update-progress"
import { Notice, Spinner } from "@/components/state"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"

/**
 * "What changed", as a panel over the page rather than a page of its own.
 *
 * The question it answers is asked from wherever the operator happens to be —
 * the sidebar notice is on every screen — and sending them to a route to read
 * release notes loses whatever they were doing. It doubles as the place an
 * upgrade is watched, for a reason that is specific to this feature: during
 * an upgrade the frontend container is being replaced too, so a click that
 * navigates anywhere is a click that may not land. Everything the operator
 * needs while their dashboard rebuilds itself has to already be on screen.
 */
export function ChangesSheet({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { can } = useAuth()
  const { report, restarting, check, checking, install } = useSelfUpdate()
  const { confirm, dialog } = useConfirm()
  const [showEarlier, setShowEarlier] = useState(false)

  const pending = useMemo(() => report?.releases ?? [], [report?.releases])
  // The history this build carries is its own past, so everything in it is at
  // or below the installed version. Anything the check turned up is shown
  // above rather than twice.
  const earlier = useMemo(() => {
    const upcoming = new Set(pending.map((r) => r.version))
    return (report?.history ?? []).filter((r) => !upcoming.has(r.version))
  }, [report?.history, pending])

  const target = report?.latest
  const canInstall = Boolean(
    can("system.admin") && report?.available && report.install.supported && !report.run?.status,
  )
  const running = report?.run?.status === "running" || report?.run?.status === "pending"

  const startInstall = () =>
    confirm({
      title: `Update to ${target}`,
      phrase: target,
      confirmLabel: "Update now",
      description: (
        <>
          <p>
            Pulls {report?.check.repo} at <b>{report?.check.ref}</b>, rebuilds every image in this
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

  return (
    <>
      <SidePanel
        open={open}
        onOpenChange={onOpenChange}
        width="md"
        icon={Puzzle}
        title={pending.length > 0 ? "What's new" : "Release notes"}
        description={
          pending.length > 0 ? (
            <>
              Just Dashboard <span className="numeric">{report?.version}</span> →{" "}
              <span className="numeric font-medium text-foreground">{target}</span>
            </>
          ) : (
            <>
              Just Dashboard <span className="numeric">{report?.version}</span> — the newest
              published version
            </>
          )
        }
        bodyClassName="space-y-5 p-4"
        footer={
          <>
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                check().catch((err) => notify.error("Check failed", errorMessage(err)))
              }
              disabled={checking || report?.check.enabled === false}
            >
              {checking ? (
                <Spinner className="size-3.5" />
              ) : (
                <RefreshClockwise className="size-3.5" />
              )}
              Check now
            </Button>
            <span className="flex-1 truncate text-hint text-muted-foreground">
              {report?.check.checkedAt ? `Checked ${relativeTime(report.check.checkedAt)}` : ""}
            </span>
            {canInstall && (
              <Button size="sm" onClick={startInstall}>
                <ArrowCircleUp className="size-3.5" />
                Update to {target}
              </Button>
            )}
            {running && (
              <Button size="sm" disabled>
                <Spinner className="size-3.5" />
                Updating
              </Button>
            )}
          </>
        }
      >
        <>
          {report?.run && (
            <UpdateProgress run={report.run} log={report.log} restarting={restarting} />
          )}

          {report?.check.enabled === false && (
            <Notice title="Version checks are turned off on this install">
              <code className="text-hint">JD_UPDATE_CHECK=false</code>, so the dashboard never asks
              whether a newer version exists. The notes below are the ones compiled into the version
              you are running.
            </Notice>
          )}
          {report?.check.error && (
            <Notice title="The last check could not reach the repository" tone="warning">
              {report.check.error}
            </Notice>
          )}

          {pending.length > 0 && (
            <div className="space-y-3">
              <p className="eyebrow">
                {pending.length === 1
                  ? "In this release"
                  : `In the ${pending.length} releases since yours`}
              </p>
              <ReleaseList releases={pending} />
            </div>
          )}

          {earlier.length > 0 && (
            <div className="space-y-3">
              {pending.length > 0 ? (
                <button
                  type="button"
                  onClick={() => setShowEarlier((v) => !v)}
                  className="flex w-full items-center gap-1.5 rounded-lg py-1 text-left focus-ring"
                >
                  <ClockRewind className="size-3.5 text-muted-foreground" />
                  <span className="eyebrow flex-1">Earlier releases ({earlier.length})</span>
                  <ChevronDown
                    className={cn(
                      "size-3.5 text-muted-foreground transition-transform",
                      showEarlier && "rotate-180",
                    )}
                  />
                </button>
              ) : (
                <p className="eyebrow">History</p>
              )}
              {(showEarlier || pending.length === 0) && (
                <ReleaseList releases={earlier} installed={report?.version} />
              )}
            </div>
          )}
        </>
      </SidePanel>
      {dialog}
    </>
  )
}

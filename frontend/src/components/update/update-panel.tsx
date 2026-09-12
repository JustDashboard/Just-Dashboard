"use client"

import { useState } from "react"
import { ArrowCircleUp, RefreshClockwise, Sparkles, Warning } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { useSelfUpdate } from "@/hooks/use-self-update"
import { useConfirm } from "@/components/confirm-dialog"
import { ChangesSheet } from "@/components/update/changes-sheet"
import { UpdateProgress } from "@/components/update/update-progress"
import { ErrorState, Notice, Spinner } from "@/components/state"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"

/**
 * The dashboard's own version, on the page that is already about updating
 * things.
 *
 * The sidebar notice is the one that finds you; this is the one you go
 * looking for, and it is the only place with room for the transcript of an
 * upgrade that has gone wrong. Both read the same poll, so they can never
 * disagree about what is happening.
 *
 * It is deliberately at the top of a page whose other half is apt: "what can
 * be updated on this machine" is one question, and answering it in two places
 * is how an operator ends up with a current server running last year's panel.
 */
export function DashboardUpdatePanel() {
  const { can } = useAuth()
  const { report, error, restarting, check, checking, install } = useSelfUpdate()
  const { confirm, dialog } = useConfirm()
  const [sheet, setSheet] = useState(false)

  if (error) return <ErrorState error={error} />
  if (!report) return null

  const run = report.run
  const running = run?.status === "running" || run?.status === "pending"
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

  return (
    <>
      {dialog}
      <Panel>
        <PanelHeader
          icon={Sparkles}
          title="Just Dashboard"
          actions={
            <>
              <Button
                variant="outline"
                size="sm"
                disabled={checking || !report.check.enabled}
                onClick={() =>
                  check().catch((err) => notify.error("Check failed", errorMessage(err)))
                }
              >
                {checking ? (
                  <Spinner className="size-4" />
                ) : (
                  <RefreshClockwise className="size-4" />
                )}
                Check now
              </Button>
              <Button variant="outline" size="sm" onClick={() => setSheet(true)}>
                {report.available ? "View changes" : "Release notes"}
              </Button>
              {report.available && canInstall && (
                <Button size="sm" onClick={startInstall}>
                  <ArrowCircleUp className="size-4" />
                  Update to {target}
                </Button>
              )}
            </>
          }
        />
        <PanelBody className="space-y-3">
          {run && <UpdateProgress run={run} log={report.log} restarting={restarting} />}

          {!running && report.available && headline && (
            <div className="space-y-1">
              <p className="text-body font-medium">
                <span className="numeric">{headline.version}</span> — {headline.title}
              </p>
              {headline.summary && (
                <p className="text-body leading-relaxed text-muted-foreground">
                  {headline.summary}
                </p>
              )}
              {report.releases.length > 1 && (
                <p className="text-xs text-muted-foreground">
                  {report.releases.length} releases since yours.
                </p>
              )}
            </div>
          )}

          {!run && !report.available && (
            <p className="text-body text-muted-foreground">
              {report.check.enabled
                ? `Checked ${relativeTime(report.check.checkedAt)} against ${report.check.repo} (${report.check.ref}).`
                : "This install never asks whether a newer version exists. The release notes below are the ones compiled into the version you are running."}
            </p>
          )}

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
          {report.available && !running && (report.install.dirty?.length ?? 0) > 0 && (
            <Notice title="The install directory has uncommitted changes" icon={Warning}>
              <p>
                {report.install.dir} carries edits that are not in git. The update fast-forwards
                rather than resetting, so they survive unless the new version changes the same lines
                — in which case it stops and tells you, rather than discarding them.
              </p>
              <ul className="mt-1 space-y-0.5 font-mono text-hint">
                {report.install.dirty?.slice(0, 6).map((line) => (
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
        </PanelBody>
      </Panel>
      <ChangesSheet open={sheet} onOpenChange={setSheet} />
    </>
  )
}

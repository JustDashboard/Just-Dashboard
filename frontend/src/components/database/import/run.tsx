"use client"

import { CloudDownload, Cross, Stop } from "@/components/icons"
import { FlowActions, FlowPanel, FlowPanelBody, FlowPanelHeader } from "@/components/flow"
import { FormNote } from "@/components/form"
import { Meter } from "@/components/meter"
import { Notice } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status, StatusDot } from "@/components/status-dot"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import {
  canCutOver,
  copyFinished,
  copyShare,
  importTitle,
  lagWords,
  progressWords,
  statusWords,
  tableLine,
  tableStateTone,
  tableStateWords,
} from "@/components/database/import/model"
import type { DbImportLive } from "@/components/database/import/types"

/**
 * The third step, and the fourth: an import under way. What it shows is what
 * the two servers say, read again every couple of seconds — the target's
 * account of each table's copy, the source's of how far behind the target is —
 * and the one thing to do next: cut over once every table is in, open the
 * database once it is whole, or start again from why it failed.
 *
 * A stream has no cutover: it is one job with a progress bar, and the page
 * leads to the database when it ends.
 */
export function RunStep({
  live,
  mayAct,
  mayStop,
  busy,
  onCutover,
  onStopReplication,
  onStopJob,
  onOpen,
  onForget,
  onNew,
}: {
  live: DbImportLive
  /** The role may cut over, stop a replication and forget an import. */
  mayAct: boolean
  /** The role may stop the job (`service.control`). */
  mayStop: boolean
  busy: boolean
  onCutover: () => void
  onStopReplication: () => void
  onStopJob: () => void
  /** Absent where the engine has no way to open a database of this server. */
  onOpen?: () => void
  onForget: () => void
  onNew: () => void
}) {
  const imp = live.import
  const { word, tone } = statusWords(imp.status)
  const subscription = live.subscription
  const share = copyShare(live)
  const running = imp.status === "running"
  const cutting = imp.status === "cutting"
  const replicating = imp.mode === "replication" && (imp.status === "replicating" || cutting)
  const finished = copyFinished(live)
  const waiting = subscription ? subscription.tables.length - subscription.ready : 0

  return (
    <div className="relative rounded-xl">
      <FlowPanel aria-label={importTitle(imp)} aria-busy={running || cutting} className="min-w-0">
        <FlowPanelHeader
          title={<span className="block truncate">{importTitle(imp)}</span>}
          actions={<Status tone={tone} label={word} />}
        />
        <FlowPanelBody className="space-y-4">
          {imp.status === "failed" && (
            <Notice tone="danger" title="It stopped">
              <span className="break-words whitespace-pre-wrap">
                {imp.detail || "The job did not finish."}
              </span>
            </Notice>
          )}
          {imp.status === "cancelled" && (
            <Notice title="It was stopped">
              <span className="break-words whitespace-pre-wrap">
                The replication was taken away from both servers.
                {imp.detail ? ` ${imp.detail}` : ""} {imp.database} still holds what was copied.
              </span>
            </Notice>
          )}
          {imp.status === "done" && (
            <Notice tone="success" title={`${imp.database} is on this server`}>
              {imp.detail || "The import finished."}
              {imp.mode === "replication" ? " It no longer follows the source." : ""}
            </Notice>
          )}
          {live.sourceError && replicating && (
            <Notice tone="warning" title="The source did not answer">
              <span className="break-words whitespace-pre-wrap">{live.sourceError}</span>
            </Notice>
          )}

          {imp.mode === "stream" && running && (
            <div className="space-y-2">
              <Meter value={share ?? 0} label="Copied" />
              <p className="numeric text-body" aria-live="polite">
                {progressWords(live.progress)}
              </p>
              {live.progress?.table && (
                <FormNote className="truncate font-mono">
                  Last finished: {live.progress.table}
                </FormNote>
              )}
            </div>
          )}

          {imp.mode === "replication" && running && !subscription && (
            <FormNote>
              Making {imp.database}, copying its structure, publishing on the source and
              subscribing. The copy of the rows starts when the subscription exists.
            </FormNote>
          )}

          {replicating && subscription && (
            <>
              <StatGrid columns={4} dense className="-mx-4 border-y border-hairline">
                <StatTile
                  label="Tables copied"
                  value={`${subscription.ready} of ${subscription.tables.length}`}
                  meter={share}
                  meterLabel="Tables copied"
                />
                <StatTile
                  label="Behind the source"
                  value={lagWords(live.lag)}
                  hint={live.lag && !live.lag.active ? "The slot is not in use" : undefined}
                  tone={live.lag && !live.lag.active ? "warning" : "default"}
                />
                <StatTile
                  label="Applying changes"
                  value={subscription.applying ? "Yes" : "No"}
                  tone={subscription.applying ? "default" : "warning"}
                />
                <StatTile
                  label="Errors"
                  value={subscription.errors < 0 ? "—" : subscription.errors.toLocaleString()}
                  hint={subscription.errors < 0 ? "Counted from PostgreSQL 15" : undefined}
                  tone={subscription.errors > 0 ? "danger" : "default"}
                />
              </StatGrid>
              <ul aria-label="Tables" className="max-h-72 divide-y divide-hairline overflow-y-auto">
                {subscription.tables.map((table) => (
                  <li
                    key={`${table.schema}.${table.name}`}
                    className="flex min-w-0 items-center gap-3 py-1.5 text-body"
                  >
                    <StatusDot tone={tableStateTone(table.state)} />
                    <span className="min-w-0 flex-1 truncate font-mono">{tableLine(table)}</span>
                    {table.state === "copying" && table.rowsCopied >= 0 && (
                      <span className="numeric text-hint text-muted-foreground">
                        {table.rowsCopied.toLocaleString()} rows
                      </span>
                    )}
                    <span className="w-24 shrink-0 text-right text-hint text-muted-foreground">
                      {tableStateWords(table.state)}
                    </span>
                  </li>
                ))}
              </ul>
            </>
          )}
          {replicating && !subscription && !cutting && (
            <Notice tone="danger" title="The subscription is not on this server">
              Nothing is following the source. Stop the replication to take what is left of it away
              from both servers.
            </Notice>
          )}
        </FlowPanelBody>

        {running && (
          <FlowActions note="It carries on if you leave this page.">
            <Button
              variant="outline"
              className="h-11 sm:h-9"
              disabled={!mayStop || !imp.job}
              onClick={onStopJob}
            >
              <Stop />
              Stop
            </Button>
          </FlowActions>
        )}
        {replicating && (
          <FlowActions
            note={
              cutting
                ? "Waiting for the target to catch up, setting the sequences and removing the subscription."
                : finished
                  ? "Stop the writes to the source, then cut over. Nothing is changed on the source."
                  : waiting > 0
                    ? `${waiting} ${waiting === 1 ? "table is" : "tables are"} still being copied. The cutover waits for all of them.`
                    : "Waiting for the first tables."
            }
            secondary={
              mayAct && (
                <Button
                  variant="outline"
                  className="h-11 sm:h-9"
                  disabled={busy || cutting}
                  onClick={onStopReplication}
                >
                  <Cross />
                  Stop replicating
                </Button>
              )
            }
          >
            <Button
              className="h-11 sm:h-9"
              disabled={!mayAct || !canCutOver(live) || busy}
              pending={cutting}
              onClick={onCutover}
            >
              Cut over…
            </Button>
          </FlowActions>
        )}
        {imp.status === "done" && (
          <FlowActions
            secondary={
              mayAct && (
                <>
                  <Button
                    variant="ghost"
                    className="h-11 sm:h-9"
                    disabled={busy}
                    onClick={onForget}
                  >
                    Forget this import
                  </Button>
                  {onOpen && (
                    <Button variant="outline" className="h-11 sm:h-9" onClick={onNew}>
                      Another import
                    </Button>
                  )}
                </>
              )
            }
          >
            {onOpen ? (
              <Button className="h-11 sm:h-9" pending={busy} onClick={onOpen}>
                <CloudDownload />
                Open {imp.database}
              </Button>
            ) : (
              <Button className="h-11 sm:h-9" disabled={!mayAct} onClick={onNew}>
                Another import
              </Button>
            )}
          </FlowActions>
        )}
        {(imp.status === "failed" || imp.status === "cancelled") && (
          <FlowActions
            secondary={
              mayAct && (
                <Button variant="ghost" className="h-11 sm:h-9" disabled={busy} onClick={onForget}>
                  Forget this import
                </Button>
              )
            }
          >
            <Button className="h-11 sm:h-9" disabled={!mayAct} onClick={onNew}>
              Start another
            </Button>
          </FlowActions>
        )}
      </FlowPanel>
      {(running || cutting) && (
        <span aria-hidden className="pointer-events-none absolute inset-0 rounded-xl">
          <BorderBeam size={96} duration={4} />
        </span>
      )}
    </div>
  )
}

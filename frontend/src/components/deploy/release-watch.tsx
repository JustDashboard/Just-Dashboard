"use client"

import { useState } from "react"
import { useNow } from "@/hooks/use-now"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight } from "@/components/icons"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentEngineRun, RollbackWatch, RollbackWatchResponse } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { SplitBar } from "@/components/rank-list"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { StatusDot } from "@/components/status-dot"
import type { Tone } from "@/components/tone"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import { WireMark, WireNode } from "@/components/wire/wire"
import {
  answeredWords,
  failedWords,
  readingLine,
  readingTone,
  trustedBaseline,
  watchLead,
  watchProxy,
  watchShown,
  watchTitle,
  windowLine,
  windowRemaining,
} from "@/components/deploy/rollback-watch-model"

/**
 * The window after a blue/green cutover, while the release it replaced is
 * kept warm (`deploy/rollback_watch.go`), in the shape every block on the
 * Overview takes (§16): the proxy's mark, since its request record is the
 * evidence, with the reading as one sentence — how much of the new release
 * fails and how long 19 in 20 of its requests take — then the two releases
 * as the reading lines the top of the page draws, each ending on its strip.
 * Under the sentence: what the reading was judged as, how long the window
 * has left and what the warm release is doing. With skew protection the warm release still answers the
 * visitors it answered first; the reading is the new release's own requests,
 * and the window's line says how many the warm one answered.
 *
 * It was four figures in a row, one of them labelled p95, on the one page the
 * operator asked to carry no metric cards. Its two verbs are the window's:
 * switch back to the warm release now, or keep the new one and give the warm
 * one's memory back. A window that rolled back stays for an hour with the
 * reading that decided it; one that closed clean is the expected answer and is
 * not drawn.
 */
export function ReleaseWatch({
  projectId,
  state,
  onChange,
}: {
  projectId: number
  state: ReleaseWatchState & { watch: RollbackWatch }
  /** Called after a verb lands, so the page's own reads catch up. */
  onChange?: () => void
}) {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [acting, setActing] = useState<"keep" | null>(null)
  const { base, watch, now } = state

  const rollBack = () =>
    confirm({
      title: "Roll back instantly",
      description: (
        <>
          Traffic goes back to release #{watch.warmReleaseNumber}&apos;s warm container, which has
          {watch.warmRunning
            ? " kept running since the cutover for the visitors it answered first,"
            : " been paused since the cutover,"}{" "}
          with release #{watch.warmReleaseNumber}&apos;s own configuration. Release #
          {watch.releaseNumber} is removed, and its visitors are answered by release #
          {watch.warmReleaseNumber}. Nothing is built or started anew.
        </>
      ),
      confirmLabel: "Roll back",
      workingLabel: "Rolling back…",
      action: async () => {
        const run = await post<DeploymentEngineRun>(`${base}/rollback`)
        onChange?.()
        router.push(`/deploy/${projectId}/runs/${run.id}`)
      },
    })

  const keep = async () => {
    setActing("keep")
    try {
      await post(`${base}/keep`)
      notify.success(
        `Kept release #${watch.releaseNumber}; release #${watch.warmReleaseNumber} was removed`,
      )
      state.refresh()
      onChange?.()
    } catch (error) {
      notify.error("Could not keep the release", error)
    } finally {
      setActing(null)
    }
  }

  const actionable = watch.state === "watching" && !watch.rollbackRunId
  const lead = watchLead(watch)
  return (
    <Panel plain data-testid="release-watch">
      <PanelHeader
        title={watchTitle(watch)}
        actions={
          watch.state === "rolled_back"
            ? watch.rollbackRunId && (
                <Link
                  href={`/deploy/${projectId}/runs/${watch.rollbackRunId}`}
                  className="inline-flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
                >
                  Open the rollback <ArrowRight className="size-3" />
                </Link>
              )
            : actionable && (
                <>
                  {can("service.control") && (
                    <Button
                      variant="outline"
                      size="sm"
                      pending={acting === "keep"}
                      disabled={acting !== null}
                      onClick={() => void keep()}
                    >
                      Keep release #{watch.releaseNumber}
                    </Button>
                  )}
                  {can("destructive") && (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={acting !== null}
                      onClick={rollBack}
                    >
                      Roll back instantly
                    </Button>
                  )}
                </>
              )
        }
      />
      <PanelBody className="space-y-4 pt-4">
        <WireNode
          mark={
            <span className="relative flex">
              <WireMark tone="logo" size="md">
                <ProductGlyph id={watchProxy(watch)} />
              </WireMark>
              <StatusDot tone={lead.tone} corner="md" />
            </span>
          }
          title={lead.title}
          hint={
            <>
              {lead.hint && (
                <span key={lead.hint} className="block animate-rise">
                  {lead.hint}
                  {watch.state === "rolled_back" && watch.endedAt && (
                    <> · {relativeTime(watch.endedAt)}</>
                  )}
                </span>
              )}
              {watch.state !== "rolled_back" && (
                <span className="numeric block" data-testid="watch-window">
                  {windowLine(watch, windowRemaining(watch, now))}
                </span>
              )}
            </>
          }
        />
        <BeforeAndAfter watch={watch} />
      </PanelBody>
      {dialog}
    </Panel>
  )
}

export type ReleaseWatchState = ReturnType<typeof useReleaseWatch>

/**
 * The environment's window, read on a four-second poll, and whether the
 * overview draws it now. The time ticks each second while a window counts
 * down, and while a rollback is on screen, so it leaves when its hour is up.
 */
export function useReleaseWatch(projectId: number, environmentId: number, enabled: boolean) {
  const base = `/deploy/${projectId}/environments/${environmentId}/rollback-watch`
  const read = usePoll(
    (signal) => get<RollbackWatchResponse>(base, undefined, signal),
    4000,
    [base],
    { enabled },
  )
  const watch = read.data?.watch ?? null
  const now = useNow(1000, Boolean(watch) && watch?.state !== "closed")
  return { base, watch, now, shown: watchShown(watch, now), refresh: read.refresh }
}

/**
 * The block's one picture: the release before and the release now as two of
 * the reading lines the top of the page draws — the release's number as its
 * mark, as the wiring draws the live release, what share of its requests
 * failed, how many it answered and how fast under it, and at the line's end
 * one strip of what it answered against what it failed. The two strips are
 * one length, so the pair still compares; a sliver of failures keeps a few
 * pixels, so half a percent is still seen.
 *
 * It was two strips across the block with their figures in a column of their
 * own at its far edge. A release that had answered nothing drew two empty
 * tracks the width of the page, which read as a picture that had not loaded;
 * now it says "No requests" in the line's place and draws no strip.
 */
function BeforeAndAfter({ watch }: { watch: RollbackWatch }) {
  const rows = [
    {
      key: "before",
      number: watch.warmReleaseNumber,
      name: "Before the cutover",
      // What the server compares against, or what little there was.
      reading: trustedBaseline(watch) ?? watch.baseline,
      tone: "default" as Tone,
      empty: "No requests",
    },
    {
      key: "since",
      number: watch.releaseNumber,
      name: "Since the cutover",
      reading: watch.observed,
      tone: readingTone(watch),
      empty: "No requests yet",
    },
  ]
  return (
    <ReadingLines data-testid="watch-picture">
      {rows.map(({ key, number, name, reading, tone, empty }) => {
        const failed = failedWords(reading)
        return (
          <ReadingLine
            key={key}
            className="animate-rise"
            mark={<ReleaseMark number={number} tone={tone} />}
            name={name}
            hint={answeredWords(reading)}
            trend={
              reading &&
              reading.requests > 0 && (
                <SplitBar
                  label={`Release #${number}, ${key}: ${readingLine(reading)}`}
                  // A failure in a few hundred is a fraction of a pixel at
                  // the strip's width; this keeps it to be seen.
                  minShare={0.03}
                  parts={[
                    {
                      key: "answered",
                      count: reading.requests - reading.failures,
                      color: "color-mix(in oklab, var(--success) 80%, transparent)",
                    },
                    { key: "failed", count: reading.failures, color: "var(--destructive)" },
                  ]}
                />
              )
            }
          >
            {failed ?? <NoReading>{empty}</NoReading>}
          </ReadingLine>
        )
      })}
    </ReadingLines>
  )
}

/**
 * A release's number on the tile the wiring gives the live release, or on
 * the warning and danger tiles once its reading is past a line.
 */
function ReleaseMark({ number, tone }: { number: number; tone: Tone }) {
  const label = <span className="numeric text-xs font-semibold">#{number}</span>
  if (tone === "warning" || tone === "danger")
    return (
      <WireMark size="md" tone={tone}>
        {label}
      </WireMark>
    )
  return (
    <WireMark size="md" tone="logo">
      {label}
    </WireMark>
  )
}

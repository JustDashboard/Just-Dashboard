"use client"

import { useEffect, useState } from "react"
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
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import { WireMark, WireNode } from "@/components/deploy/wire"
import {
  readingLine,
  shareOf,
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
 * fails and how long 19 in 20 of its requests take — then one picture, the
 * two releases as two strips of one length. Under the sentence: what the
 * reading was judged as, how long the window has left and what the warm
 * release is doing. With skew protection the warm release still answers the
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
              <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
                <StatusDot tone={lead.tone} />
              </span>
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
  const now = useNow(Boolean(watch) && watch?.state !== "closed")
  return { base, watch, now, shown: watchShown(watch, now), refresh: read.refresh }
}

/**
 * The block's one picture: the release before and the release now, each one
 * strip of the same length split into what it answered and what it failed, with
 * the figures the strip stands for in a column that lines up down the pair.
 * A sliver of failures keeps a few pixels, so half a percent is still seen.
 */
function BeforeAndAfter({ watch }: { watch: RollbackWatch }) {
  const rows = [
    {
      key: "before",
      name: `Release #${watch.warmReleaseNumber}, before`,
      // What the server compares against, or what little there was.
      reading: trustedBaseline(watch) ?? watch.baseline,
    },
    { key: "now", name: `Release #${watch.releaseNumber}, since`, reading: watch.observed },
  ]
  return (
    <ul className="min-w-0 space-y-2" data-testid="watch-picture">
      {rows.map(({ key, name, reading }) => {
        const failed = shareOf(reading) ?? 0
        const words = readingLine(reading)
        return (
          <li
            key={key}
            data-slot="watch-row"
            className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 sm:h-5 sm:grid-cols-[9.5rem_minmax(0,1fr)_15rem]"
          >
            <span className="truncate text-hint text-muted-foreground">{name}</span>
            <span className="numeric truncate text-right text-hint text-foreground sm:order-3">
              {words}
            </span>
            <span
              role="img"
              aria-label={`${name}: ${words}`}
              className="flex h-2.5 min-w-0 animate-rise gap-0.5 overflow-hidden rounded-sm bg-meter-track max-sm:col-span-2 sm:order-2"
            >
              {reading && reading.requests > 0 && (
                <>
                  <span className="block h-full min-w-0 flex-1 bg-success/80 [transition:flex-grow_700ms_ease-out]" />
                  {failed > 0 && (
                    <span
                      className="block h-full bg-destructive [transition:width_700ms_ease-out]"
                      style={{ width: `${failed}%`, minWidth: 4 }}
                    />
                  )}
                </>
              )}
            </span>
          </li>
        )
      })}
    </ul>
  )
}

/** The time, ticking each second while a window counts down. */
function useNow(ticking: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!ticking) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [ticking])
  return now
}

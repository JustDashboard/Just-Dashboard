"use client"

import { useMemo, useState } from "react"
import {
  ArrowCircleUp,
  CheckCircle,
  ClockRewind,
  Copy,
  GitBranch,
  Play,
  Pencil,
  RefreshClockwise,
  RotateClockwise,
  StopCircle,
  Trash,
  Warning,
} from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { calendarDate, clockMinute, plural, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { StackDeployment } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useNow } from "@/components/deploy/vocabulary"
import { AuthorMark } from "@/components/git/marks"
import { ago } from "@/components/procs/units"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { ComposeDiff, DiffCount, ServiceLabel } from "@/components/docker/stack-diff"
import {
  configHues,
  diffCounts,
  doneWord,
  historySpans,
  lineDiff,
  sameFile,
  shortDigest,
  type HistorySpan,
} from "@/components/docker/stack-views"

/** Each action's mark, in the colour of what it costs: the run console's verbs. */
const MARK: Record<string, { icon: React.ComponentType<{ className?: string }>; text: string }> = {
  up: { icon: Play, text: "text-brand" },
  update: { icon: ArrowCircleUp, text: "text-[var(--chart-2)]" },
  restart: { icon: RotateClockwise, text: "text-[var(--tag-cyan)]" },
  recreate: { icon: RefreshClockwise, text: "text-warning" },
  start: { icon: Play, text: "text-success" },
  stop: { icon: StopCircle, text: "text-muted-foreground" },
  down: { icon: Trash, text: "text-destructive" },
}

const markOf = (action?: string) => MARK[action ?? "up"] ?? MARK.up

/**
 * What this dashboard wrote down each time it changed the stack — which
 * exists because Docker keeps nothing.
 *
 * It was a column of disclosures that each read "restart · 2d 13s ago" over
 * a hash. Now it opens on time: a strip from the first record to now, each
 * stretch in the colour of the compose file that was live through it, so
 * "the file changed here, and these three deploys ran the same one" is seen
 * at once. Under it the records are a rail, the shape a backup job's runs
 * take, and the one picked is read whole beside them: who did what from
 * which commit, whether it could be put back, how its file differs from the
 * one on disk now, and the images that were running just before.
 */
export function StackHistory({
  stack,
  epoch,
  productOf,
  onRestore,
}: {
  stack: string
  /** Changes after each compose command the page runs, which writes a record. */
  epoch: number
  productOf: (service: string) => string | undefined
  /** Puts a record's file in the editor, unsaved; absent where the reader may not write it. */
  onRestore?: (content: string, from: string) => void
}) {
  const wide = useMediaQuery("(min-width: 1280px)")
  const now = useNow(60_000)
  const { data, error, loading } = usePoll<StackDeployment[]>(
    (signal) =>
      get<StackDeployment[]>(
        `/docker/stacks/${encodeURIComponent(stack)}/deployments`,
        undefined,
        signal,
      ),
    60_000,
    [stack, epoch],
  )
  // The file on disk, once for the tab: each record is read against it.
  const current = usePoll<{ content: string }>(
    (signal) =>
      get<{ content: string }>(
        `/docker/stacks/${encodeURIComponent(stack)}/config`,
        undefined,
        signal,
      ),
    0,
    [stack, epoch],
  )
  const [picked, setPicked] = useState<number>()
  const records = useMemo(() => data ?? [], [data])
  const spans = useMemo(() => historySpans(records, now), [records, now])
  const hues = useMemo(() => configHues(records), [records])

  if (loading && !data) return <LoadingRows />
  if (error) return <ErrorState error={error} />
  if (records.length === 0) {
    return (
      <EmptyState
        icon={ClockRewind}
        title="No deployments recorded yet"
        description="The dashboard writes a stack down just before it changes it. Nothing has been deployed through here yet, so there is nothing to look back on."
      />
    )
  }

  const selected = records.find((r) => r.id === picked) ?? records[0]
  const files = new Set(records.map((r) => r.configHash)).size
  const newest = records[0]

  return (
    <div className="flex min-w-0 animate-rise flex-col gap-6 pb-6">
      <section aria-label="Configurations over time" className="flex min-w-0 flex-col gap-2.5">
        <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-6 gap-y-1">
          <p className="text-body">
            <span className="font-medium">{plural(records.length, "deployment")}</span>
            <span className="text-muted-foreground">
              {" "}
              of {plural(files, "compose file")} · the last{" "}
              {ago(Date.parse(newest.createdAt) / 1000, now)}
              {newest.actor && ` by ${newest.actor}`}
            </span>
          </p>
          <p className="text-hint text-muted-foreground">Each colour is one version of the file</p>
        </div>
        <Timeline
          spans={spans}
          records={records}
          hues={hues}
          selected={selected.id}
          onPick={setPicked}
        />
        <div className="flex justify-between text-hint text-muted-foreground">
          <span>{calendarDate(records.at(-1)?.createdAt)}</span>
          <span>now</span>
        </div>
      </section>

      <div
        className={cn("grid min-w-0 gap-6", wide && "grid-cols-[22rem_minmax(0,1fr)] items-start")}
      >
        <Panel aria-label="Deployments">
          <PanelHeader
            title={
              <>
                Deployments
                <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                  {records.length}
                </span>
              </>
            }
          />
          <PanelBody className="p-1.5">
            <Rail
              records={records}
              spans={spans}
              hues={hues}
              selected={selected.id}
              now={now}
              onPick={setPicked}
            />
          </PanelBody>
          <PanelFooter className="text-hint text-muted-foreground">
            Environment values are hashed, never stored.
          </PanelFooter>
        </Panel>
        <Detail
          key={selected.id}
          stack={stack}
          record={selected}
          hue={hues.get(selected.configHash)}
          current={current.data?.content}
          now={now}
          productOf={productOf}
          onRestore={onRestore}
        />
      </div>
    </div>
  )
}

/**
 * The records laid on time, oldest on the left: each stretch runs to the
 * next record and wears its file's colour, and each record is a mark at the
 * start of its stretch. Pressing a stretch reads that record.
 */
function Timeline({
  spans,
  records,
  hues,
  selected,
  onPick,
}: {
  spans: HistorySpan[]
  records: StackDeployment[]
  hues: Map<string, string>
  selected: number
  onPick: (id: number) => void
}) {
  const byId = new Map(records.map((r) => [r.id, r]))
  return (
    <div className="relative h-7 w-full">
      <div className="absolute inset-x-0 top-2.5 h-2 rounded-full bg-meter-track" />
      {spans.map((span) => {
        const record = byId.get(span.id)!
        const here = span.id === selected
        const label = `${doneWord(record.action)}, ${timestamp(record.createdAt)}`
        return (
          <button
            key={span.id}
            type="button"
            aria-label={label}
            title={label}
            aria-pressed={here}
            onClick={() => onPick(span.id)}
            className="group absolute top-0 h-7 focus-ring"
            style={{ left: `${span.start * 100}%`, width: `max(${span.width * 100}%, 0.75rem)` }}
          >
            <span
              aria-hidden
              className={cn(
                "absolute inset-x-px top-2.5 h-2 rounded-sm transition-[filter]",
                here ? "saturate-100" : "saturate-[.6] group-hover:saturate-100",
              )}
              style={{ background: hues.get(span.hash) }}
            />
            <span
              aria-hidden
              className={cn(
                "absolute top-1 -left-1.5 size-3 rounded-full border-2 border-background transition-transform",
                here ? "scale-125" : "group-hover:scale-110",
              )}
              style={{ background: hues.get(span.hash) }}
            />
          </button>
        )
      })}
    </div>
  )
}

function Rail({
  records,
  spans,
  hues,
  selected,
  now,
  onPick,
}: {
  records: StackDeployment[]
  spans: HistorySpan[]
  hues: Map<string, string>
  selected: number
  now: number
  onPick: (id: number) => void
}) {
  const changed = new Map(spans.map((s) => [s.id, s.changed]))
  return (
    <ol aria-label="Deployments" className="min-w-0 space-y-0.5">
      {records.map((record) => {
        const here = record.id === selected
        const { icon: Icon, text } = markOf(record.action)
        return (
          <li key={record.id} className="min-w-0">
            <button
              type="button"
              data-workspace-item={`deployment:${record.id}`}
              data-workspace-name={`${doneWord(record.action)} ${calendarDate(record.createdAt)}`}
              aria-current={here || undefined}
              onClick={() => onPick(record.id)}
              className={cn(
                "flex w-full min-w-0 items-start gap-2.5 rounded-lg px-2.5 py-2 text-left focus-ring-inset transition-colors",
                here ? "bg-accent" : "hover:bg-row-hover",
              )}
            >
              <span
                className={cn(
                  "mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-md border border-hairline bg-background",
                  text,
                )}
              >
                <Icon aria-hidden className="size-3.5" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="flex min-w-0 items-baseline justify-between gap-2">
                  <span className="truncate text-body font-medium">{doneWord(record.action)}</span>
                  <span className="numeric shrink-0 text-hint text-muted-foreground">
                    {ago(Date.parse(record.createdAt) / 1000, now)}
                  </span>
                </span>
                <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
                  <AuthorMark name={record.actor} />
                  <span className="truncate">{record.actor ?? "someone"}</span>
                  {record.gitCommit && (
                    <>
                      <span className="text-muted-foreground/40">·</span>
                      <span className="truncate font-mono">{record.gitCommit.slice(0, 7)}</span>
                      {record.gitDirty && (
                        <span
                          title="The checkout had uncommitted changes"
                          className="size-1.5 shrink-0 rounded-full bg-warning"
                        />
                      )}
                    </>
                  )}
                  <span className="ml-auto flex shrink-0 items-center gap-1">
                    <span
                      aria-hidden
                      className="size-2 rounded-sm"
                      style={{ background: hues.get(record.configHash) }}
                    />
                    <span className="font-mono">{record.configHash.slice(0, 7)}</span>
                  </span>
                </span>
                {changed.get(record.id) && (
                  <span className="mt-1 block text-hint text-foreground">
                    A different file from the one before
                  </span>
                )}
              </span>
            </button>
          </li>
        )
      })}
    </ol>
  )
}

/**
 * One record read whole. The file it holds is the one compose read for that
 * action, and the digests are what each service was running just before it.
 */
function Detail({
  stack,
  record,
  hue,
  current,
  now,
  productOf,
  onRestore,
}: {
  stack: string
  record: StackDeployment
  hue?: string
  /** The compose file on disk now. */
  current?: string
  now: number
  productOf: (service: string) => string | undefined
  onRestore?: (content: string, from: string) => void
}) {
  const detail = usePoll<StackDeployment>(
    (signal) =>
      get<StackDeployment>(
        `/docker/stacks/${encodeURIComponent(stack)}/deployments/${record.id}`,
        undefined,
        signal,
      ),
    0,
    [stack, record.id],
  )
  const full = detail.data
  const since = useMemo(
    () =>
      full?.config !== undefined && current !== undefined
        ? lineDiff(full.config, current)
        : undefined,
    [full, current],
  )
  const counts = diffCounts(since ?? [])
  const when = ago(Date.parse(record.createdAt) / 1000, now)
  const { icon: Icon, text } = markOf(record.action)

  return (
    <div className="flex min-w-0 flex-col gap-6" aria-label="The deployment">
      <header className="flex min-w-0 flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="flex min-w-0 items-start gap-3">
          <span
            className={cn(
              "flex size-8 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background",
              text,
            )}
          >
            <Icon aria-hidden className="size-4" />
          </span>
          <div className="min-w-0">
            <h2 className="text-title leading-tight font-semibold tracking-tight">
              {doneWord(record.action)} {when}
            </h2>
            <p className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-hint text-muted-foreground">
              <span className="numeric">
                {calendarDate(record.createdAt)} {clockMinute(record.createdAt)}
              </span>
              {record.actor && (
                <span className="flex items-center gap-1.5">
                  <span className="text-muted-foreground/40">·</span>
                  <AuthorMark name={record.actor} />
                  <span className="text-foreground">{record.actor}</span>
                </span>
              )}
              {record.gitCommit && (
                <span className="flex items-center gap-1.5">
                  <span className="text-muted-foreground/40">·</span>
                  <GitBranch aria-hidden className="size-3" />
                  <span>
                    {record.gitBranch && `${record.gitBranch} @ `}
                    <span className="font-mono text-foreground">
                      {record.gitCommit.slice(0, 7)}
                    </span>
                  </span>
                  {record.gitDirty && (
                    <span className="text-warning">with uncommitted changes</span>
                  )}
                </span>
              )}
              <span className="flex items-center gap-1.5">
                <span className="text-muted-foreground/40">·</span>
                <span aria-hidden className="size-2 rounded-sm" style={{ background: hue }} />
                <span className="font-mono">file {record.configHash.slice(0, 7)}</span>
              </span>
            </p>
          </div>
        </div>
        <span className="flex shrink-0 flex-wrap items-center gap-1.5">
          {onRestore && full?.config && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => onRestore(full.config ?? "", `the deployment ${when}`)}
            >
              <Pencil className="size-3.5" />
              Edit from this version
            </Button>
          )}
          {full?.config && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => void copyText(full.config ?? "", "Compose file copied")}
            >
              <Copy className="size-3.5" />
              Copy the file
            </Button>
          )}
        </span>
      </header>

      {detail.loading && !full && <LoadingRows rows={3} />}
      {detail.error && <ErrorState error={detail.error} />}
      {full && (
        <>
          {/*
            Whether a rollback is even possible. An image that is gone from
            this host and from its registry cannot be restored, and saying
            so before the operator commits is the difference between a
            rollback and an outage.
          */}
          <p
            className={cn(
              "flex items-start gap-2 text-body leading-relaxed",
              full.restorable ? "text-muted-foreground" : "text-warning",
            )}
          >
            {full.restorable ? (
              <CheckCircle aria-hidden className="mt-0.5 size-3.5 shrink-0 text-success" />
            ) : (
              <Warning aria-hidden className="mt-0.5 size-3.5 shrink-0" />
            )}
            <span>
              {full.restorable
                ? "Every image it ran is still on this server, so this configuration can be brought back exactly."
                : `${full.missing?.join(", ")} ${full.missing?.length === 1 ? "is" : "are"} no longer on this server, so bringing this back would pull whatever those tags point at now.`}
            </span>
          </p>

          <Panel plain aria-label="Since then">
            <PanelHeader
              title="What the file has changed since"
              actions={
                since &&
                since.length > 0 && <DiffCount added={counts.added} removed={counts.removed} />
              }
            />
            <PanelBody className="pt-2">
              {!since ? (
                <LoadingRows rows={2} />
              ) : since.length === 0 || sameFile(full.config, current) ? (
                <EmptyNote className="px-0 py-2 text-left">
                  The compose file on disk is this one.
                </EmptyNote>
              ) : (
                <ComposeDiff lines={since} productOf={productOf} />
              )}
            </PanelBody>
          </Panel>

          <Panel plain aria-label="Images running just before">
            <PanelHeader title="Images running just before" />
            <PanelBody className="pt-1">
              {Object.keys(full.imageDigests).length === 0 ? (
                <EmptyNote className="px-0 py-2 text-left">
                  Nothing was running, so there were no images to record.
                </EmptyNote>
              ) : (
                <ul className="divide-y divide-hairline">
                  {Object.entries(full.imageDigests).map(([service, digest]) => {
                    const short = shortDigest(digest)
                    return (
                      <li key={service} className="group flex min-w-0 items-center gap-3 py-2">
                        <ServiceLabel
                          name={service}
                          product={productOf(service)}
                          className="w-36 shrink-0"
                        />
                        <span
                          className="min-w-0 flex-1 truncate font-mono text-hint"
                          title={digest}
                        >
                          {short.repo && (
                            <span className="text-muted-foreground">{short.repo}@</span>
                          )}
                          {short.hash}
                        </span>
                        <Button
                          size="icon-xs"
                          variant="ghost"
                          aria-label={`Copy ${service} digest`}
                          onClick={() => void copyText(digest, "Digest copied")}
                        >
                          <Copy />
                        </Button>
                      </li>
                    )
                  })}
                </ul>
              )}
            </PanelBody>
          </Panel>
        </>
      )}
    </div>
  )
}

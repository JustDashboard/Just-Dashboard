"use client"

import { Fragment, useState } from "react"
import { ChevronDown, ChevronRight } from "@/components/icons"
import { relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FieldRow } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyState, LoadingPanel } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { nameHue } from "@/components/database/home/kinds"
import { EngineMark } from "@/components/database/kit"
import { mongoProfiler, setProfiler } from "@/components/database/mongo/api"
import { DatabaseMark } from "@/components/database/mongo/rail"
import type {
  MongoDatabase,
  MongoProfilerEntry,
  MongoProfilerLevel,
} from "@/components/database/mongo/types"
import type { Mongo } from "@/components/database/mongo/use-mongo"
import { ReadError } from "@/components/database/redis/read-error"

const LEVELS: { value: `${MongoProfilerLevel}`; label: string }[] = [
  { value: "0", label: "Off" },
  { value: "1", label: "Slow operations" },
  { value: "2", label: "Every operation" },
]
const LEVEL_HINT: Record<MongoProfilerLevel, string> = {
  0: "Nothing is recorded. What was recorded before is kept.",
  1: "Operations slower than the threshold are recorded.",
  2: "Every operation is recorded, which costs the server throughput. For a short look only.",
}

/**
 * What an operation does, by its kind. The hues are the ones the section
 * gives a statement's first word wherever statements are listed: a read
 * blue, an insert green, a change violet, a removal pink.
 */
const OP_HUE: Record<string, string> = {
  query: "text-(--tag-blue)",
  getmore: "text-(--tag-blue)",
  command: "text-(--tag-blue)",
  count: "text-(--tag-blue)",
  distinct: "text-(--tag-blue)",
  insert: "text-(--tag-green)",
  update: "text-(--tag-violet)",
  remove: "text-(--tag-pink)",
  delete: "text-(--tag-pink)",
}

const grouped = (n: number) => n.toLocaleString("en-US")

function took(millis: number): string {
  if (millis >= 60_000) return `${(millis / 60_000).toFixed(1)} min`
  if (millis >= 1000) return `${(millis / 1000).toFixed(millis >= 10_000 ? 0 : 1)} s`
  return `${grouped(millis)} ms`
}

/**
 * The slow operations a database's profiler has recorded, and the profiler's
 * own settings.
 *
 * The profiler belongs to one database: its level is that database's alone.
 * The slow threshold is not — it is one for the whole server, and it also
 * decides what the server writes to its log as slow — so changing it here
 * changes it for every database. Both are an administrator's to set.
 */
export function ProfilerView({
  mongo,
  databases,
}: {
  mongo: Mongo
  databases: PollState<MongoDatabase[]>
}) {
  const { id, engine, admin, readOnly, select, database } = mongo
  const profiler = usePoll((signal) => mongoProfiler(id, database, signal), 15_000, [id, database])
  const [threshold, setThreshold] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState<string | null>(null)
  const data = profiler.data
  const maySet = admin && !readOnly

  const names = databases.data?.map((entry) => entry.name) ?? []
  const listed = database && !names.includes(database) ? [database, ...names] : names
  const picker = (
    <Select value={database} onValueChange={(db) => select({ db })}>
      <SelectTrigger
        size="sm"
        aria-label="Database whose profiler is shown"
        className="h-8 max-w-56 gap-1.5 px-2 font-mono text-xs data-[size=sm]:h-8 sm:data-[size=sm]:h-8"
      >
        <SelectValue placeholder="Choose a database" />
      </SelectTrigger>
      <SelectContent position="popper" align="end" className="max-h-72">
        {listed.map((name) => (
          <SelectItem key={name} value={name} className="font-mono text-xs">
            <DatabaseMark name={name} />
            {name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )

  if (profiler.error && !data) {
    return (
      <Panel plain>
        <PanelHeader title="Slow operations" actions={picker} />
        <ReadError error={profiler.error} onRetry={profiler.refresh} className="mt-4" />
      </Panel>
    )
  }
  if (!data) return <LoadingPanel plain rows={5} />

  const change = async (patch: { level?: MongoProfilerLevel; slowMs?: number }, said: string) => {
    setBusy(true)
    try {
      await setProfiler(id, data.database, patch)
      notify.success(said)
      setThreshold(null)
      profiler.refresh()
    } catch (err) {
      notify.error("Could not change the profiler", err)
    } finally {
      setBusy(false)
    }
  }

  const typed = threshold ?? String(data.slowMs)
  const thresholdBad = !/^\d+$/.test(typed.trim())
  const entries = data.entries

  return (
    <Panel plain>
      <PanelHeader title="Slow operations" actions={picker} />
      {profiler.error && (
        <p role="status" className="pt-3 text-hint text-warning">
          The profiler could not be read again just now: {profiler.error.message}
        </p>
      )}
      <div className="pt-4">
        <FieldRow>
          <Field label="Record" hint={LEVEL_HINT[data.level]}>
            {maySet ? (
              <Segments
                label="Profiler level"
                value={`${data.level}`}
                disabled={busy}
                options={LEVELS}
                onChange={(next) => {
                  const level = Number(next) as MongoProfilerLevel
                  void change(
                    { level },
                    level === 0
                      ? `The profiler of ${data.database} is off`
                      : level === 1
                        ? `Slow operations of ${data.database} are being recorded`
                        : `Every operation of ${data.database} is being recorded`,
                  )
                }}
              />
            ) : (
              <p className="flex h-8 items-center text-body">
                {LEVELS[data.level]?.label ?? "Off"}
              </p>
            )}
          </Field>
          <Field
            label="Slower than"
            htmlFor="mongo-profiler-slowms"
            hint="Milliseconds. One threshold for the whole server: it is also what the server logs as slow."
            error={thresholdBad ? "A whole number of milliseconds." : undefined}
          >
            {maySet ? (
              <div className="flex items-center gap-2">
                <Input
                  id="mongo-profiler-slowms"
                  inputMode="numeric"
                  value={typed}
                  className="numeric w-28"
                  onChange={(event) => setThreshold(event.target.value)}
                />
                <Button
                  size="sm"
                  variant="outline"
                  pending={busy}
                  disabled={thresholdBad || Number(typed) === data.slowMs}
                  onClick={() =>
                    void change(
                      { slowMs: Number(typed) },
                      `Operations slower than ${Number(typed)} ms count as slow on this server`,
                    )
                  }
                >
                  Set
                </Button>
              </div>
            ) : (
              <p className="numeric flex h-8 items-center text-body">{grouped(data.slowMs)} ms</p>
            )}
          </Field>
        </FieldRow>
      </div>

      <PanelBody flush className="mt-4 group-data-[plain]/panel:-mx-4">
        {entries.length === 0 ? (
          <EmptyState
            mark={<EngineMark engine={engine} />}
            className="mx-4 border-0"
            title={
              data.level === 0
                ? `The profiler of ${data.database} is off`
                : `Nothing slow has been recorded in ${data.database}`
            }
            description={
              data.level === 0
                ? `No operation has been recorded. Turned on, it keeps every operation slower than ${grouped(data.slowMs)} ms.`
                : `No operation has taken longer than ${grouped(data.slowMs)} ms since the profiler was turned on.`
            }
            action={
              data.level === 0 &&
              maySet && (
                <Button
                  size="sm"
                  variant="outline"
                  pending={busy}
                  onClick={() =>
                    void change(
                      { level: 1 },
                      `Slow operations of ${data.database} are being recorded`,
                    )
                  }
                >
                  Record slow operations
                </Button>
              )
            }
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Operation</TableHead>
                <TableHead className="text-right">Took</TableHead>
                <TableHead className="text-right">Examined</TableHead>
                <TableHead className="text-right">Returned</TableHead>
                <TableHead className="max-lg:hidden">Plan</TableHead>
                <TableHead className="text-right">When</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((entry, index) => {
                const key = `${entry.time}:${index}`
                const shown = open === key
                return (
                  <Fragment key={key}>
                    <TableRow
                      data-slot="mongo-slow-operation"
                      data-state={shown ? "selected" : undefined}
                      onActivate={() => setOpen(shown ? null : key)}
                    >
                      <TableCell className="max-w-96">
                        <button
                          type="button"
                          aria-expanded={shown}
                          className="flex max-w-full min-w-0 items-center gap-1.5 rounded-sm text-left focus-ring"
                          onClick={(event) => {
                            event.stopPropagation()
                            setOpen(shown ? null : key)
                          }}
                        >
                          {shown ? (
                            <ChevronDown className="size-3 shrink-0 text-muted-foreground" />
                          ) : (
                            <ChevronRight className="size-3 shrink-0 text-muted-foreground" />
                          )}
                          <span className="min-w-0 truncate font-mono font-medium">
                            <span className={OP_HUE[entry.op]}>{entry.op}</span> {entry.ns}
                          </span>
                        </button>
                      </TableCell>
                      <TableCell className="numeric text-right font-medium whitespace-nowrap">
                        {took(entry.millis)}
                      </TableCell>
                      <TableCell className="numeric text-right whitespace-nowrap text-muted-foreground">
                        {grouped(entry.docsExamined)} docs · {grouped(entry.keysExamined)} keys
                      </TableCell>
                      <TableCell className="numeric text-right">
                        {grouped(entry.returned)}
                      </TableCell>
                      <TableCell
                        className={cn(
                          "max-w-56 truncate font-mono max-lg:hidden",
                          entry.planSummary.startsWith("COLLSCAN")
                            ? "text-warning"
                            : "text-muted-foreground",
                        )}
                      >
                        {entry.planSummary || "—"}
                      </TableCell>
                      <TableCell
                        className="text-right whitespace-nowrap text-muted-foreground"
                        title={timestamp(entry.time)}
                      >
                        {relativeTime(entry.time)}
                      </TableCell>
                    </TableRow>
                    {shown && (
                      <TableRow className="hover:bg-transparent">
                        <TableCell colSpan={6} className="pt-0">
                          <SlowDetail entry={entry} />
                        </TableCell>
                      </TableRow>
                    )}
                  </Fragment>
                )
              })}
            </TableBody>
          </Table>
        )}
      </PanelBody>
    </Panel>
  )
}

function SlowDetail({ entry }: { entry: MongoProfilerEntry }) {
  const who = entry.appName || entry.user
  return (
    <div className="animate-rise space-y-2 pb-1">
      <Well className="max-h-48 overflow-auto text-hint leading-relaxed break-all whitespace-pre-wrap">
        {entry.command}
        {entry.commandTruncated ? "…" : ""}
      </Well>
      <div className="flex flex-wrap items-start gap-x-8 gap-y-2">
        <DetailList>
          <Detail label="At">{timestamp(entry.time)}</Detail>
          <Detail label="For">
            {who ? <span style={{ color: nameHue(who) }}>{who}</span> : "—"}
            {entry.client ? <span className="font-mono"> · {entry.client}</span> : ""}
          </Detail>
          <Detail label="Plan">
            <span className="font-mono">{entry.planSummary || "—"}</span>
          </Detail>
        </DetailList>
        <DetailList>
          <Detail label="Changed">
            {grouped(entry.inserted)} inserted · {grouped(entry.modified)} modified ·{" "}
            {grouped(entry.deleted)} deleted
          </Detail>
          <Detail label="Reply">{grouped(entry.responseLength)} bytes</Detail>
          <Detail label="Gave way">{grouped(entry.numYields)} times</Detail>
        </DetailList>
        <span className="flex flex-wrap items-center gap-2.5 pt-0.5">
          {entry.hasSortStage && <Tag tone="warning">sorted in memory</Tag>}
          {entry.usedDisk && <Tag tone="warning">spilled to disk</Tag>}
        </span>
      </div>
      {entry.error && (
        <p className="text-hint text-destructive">The operation failed: {entry.error}</p>
      )}
    </div>
  )
}

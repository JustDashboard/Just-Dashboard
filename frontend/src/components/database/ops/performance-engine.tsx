"use client"

import { useMemo, useState } from "react"
import { cn } from "@/lib/utils"
import { bytes, duration, relativeTime, timestamp } from "@/lib/format"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { Disclosure, FormNote } from "@/components/form"
import { Meter } from "@/components/meter"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { compact } from "@/components/database/home/readings"
import {
  readChMerges,
  readChMutations,
  readChParts,
  readSqliteFile,
} from "@/components/database/ops/performance-api"
import { MaintenanceList, useMaintenance } from "@/components/database/ops/performance-maintenance"
import {
  NoFigure,
  Stale,
  StatementLine,
  ViewRead,
} from "@/components/database/ops/performance-parts"
import type { ChPartition } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How much smaller a partition is on disk than its data. */
function ratio(partition: Pick<ChPartition, "bytes" | "uncompressedBytes">): string | undefined {
  if (partition.bytes <= 0 || partition.uncompressedBytes <= 0) return undefined
  return `${(partition.uncompressedBytes / partition.bytes).toFixed(1)}×`
}

/**
 * What a MergeTree table is made of on disk: its partitions, and in each the
 * parts the server has yet to merge.
 *
 * Every insert writes a part and the server merges parts in the background;
 * a partition whose parts keep piling up is one whose inserts are arriving
 * faster than they are merged, and inserts start being delayed and then
 * refused. So a partition is read by how many parts it holds, beside what it
 * weighs. The parts themselves are reference, and fold under the partitions.
 */
export function PartsView() {
  const { id } = useDatabase()
  const [table, setTable] = useState("")
  const [open, setOpen] = useViewState(`databases.${id}.performance.parts`, false)
  const parts = usePoll((signal) => readChParts(id, {}, signal), 30_000, [id])
  const partitions = parts.data?.partitions
  const tables = useMemo(() => {
    const counts = new Map<string, number>()
    for (const partition of partitions ?? []) {
      counts.set(partition.table, (counts.get(partition.table) ?? 0) + partition.parts)
    }
    return [...counts.entries()].sort((a, b) => b[1] - a[1])
  }, [partitions])
  const largest = (partitions ?? []).reduce((most, entry) => Math.max(most, entry.bytes), 0)

  return (
    <Panel plain aria-label="Parts">
      <PanelHeader title="Partitions and parts" actions={<Stale poll={parts} />} />
      <ViewRead poll={parts} what="the parts" skeleton={<LoadingPanel plain rows={5} />}>
        {(data) => {
          const shown = data.partitions.filter((entry) => !table || entry.table === table)
          const listed = data.parts.filter((entry) => !table || entry.table === table)
          return (
            <div className="animate-rise space-y-3 pt-3">
              {tables.length > 1 && (
                <ChipStrip role="group" aria-label="Narrow to one table">
                  <FilterChip selected={!table} onClick={() => setTable("")}>
                    Every table
                  </FilterChip>
                  {tables.map(([name, count]) => (
                    <FilterChip
                      key={name}
                      selected={table === name}
                      onClick={() => setTable(table === name ? "" : name)}
                    >
                      <span className="font-mono">{name}</span>
                      <ChipCount>{count.toLocaleString()}</ChipCount>
                    </FilterChip>
                  ))}
                </ChipStrip>
              )}
              <PanelBody flush className="group-data-[plain]/panel:-mx-4">
                {shown.length === 0 ? (
                  <EmptyNote className="px-4">
                    No table of <span className="font-mono">{data.database}</span> has a part yet:
                    parts are written by inserts.
                  </EmptyNote>
                ) : (
                  <Table>
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        <TableHead>Partition</TableHead>
                        <TableHead className="px-2 text-right">Parts</TableHead>
                        <TableHead className="px-2 text-right">Rows</TableHead>
                        <TableHead className="px-2">On disk</TableHead>
                        <TableHead className="px-2 text-right max-sm:hidden">Compressed</TableHead>
                        <TableHead className="max-md:hidden">Last written</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {shown.map((partition) => (
                        <TableRow key={`${partition.table}:${partition.partition}`}>
                          <TableCell className="py-2">
                            <span className="flex items-center gap-2">
                              <span className="font-mono">{partition.table}</span>
                              <Tag mono>{partition.partition || "all"}</Tag>
                            </span>
                          </TableCell>
                          <TableCell className="numeric px-2 py-2 text-right">
                            {partition.parts.toLocaleString()}
                          </TableCell>
                          <TableCell className="numeric px-2 py-2 text-right">
                            {compact(partition.rows)}
                          </TableCell>
                          <TableCell className="px-2 py-2">
                            <span className="flex min-w-32 items-center gap-2.5">
                              <span
                                aria-hidden
                                className="relative h-1 min-w-10 flex-1 overflow-hidden rounded-full bg-meter-track"
                              >
                                <span
                                  className="absolute inset-y-0 left-0 rounded-full bg-(--chart-1)"
                                  style={{
                                    width: `${largest > 0 ? Math.max((partition.bytes / largest) * 100, 2) : 0}%`,
                                  }}
                                />
                              </span>
                              <span className="numeric w-16 shrink-0 text-right">
                                {bytes(partition.bytes)}
                              </span>
                            </span>
                          </TableCell>
                          <TableCell className="numeric px-2 py-2 text-right text-muted-foreground max-sm:hidden">
                            {ratio(partition) ?? <NoFigure />}
                          </TableCell>
                          <TableCell
                            className="py-2 text-muted-foreground max-md:hidden"
                            title={timestamp(partition.modified)}
                          >
                            {partition.modified ? relativeTime(partition.modified) : <NoFigure />}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </PanelBody>
              {data.truncated && (
                <FormNote>
                  The 500 largest partitions and parts are listed; there are more.
                </FormNote>
              )}
              {listed.length > 0 && (
                <Disclosure
                  quiet
                  open={open}
                  onOpenChange={setOpen}
                  summary={<>Every part · {listed.length.toLocaleString()}</>}
                >
                  {open && (
                    <div className="-mx-4">
                      <Table containerClassName="max-h-[28rem]">
                        <TableHeader>
                          <TableRow className="hover:bg-transparent">
                            <TableHead>Part</TableHead>
                            <TableHead className="px-2">Of</TableHead>
                            <TableHead className="px-2 text-right">Rows</TableHead>
                            <TableHead className="px-2 text-right">On disk</TableHead>
                            <TableHead className="px-2 text-right max-sm:hidden">Merged</TableHead>
                            <TableHead className="max-md:hidden">Stored as</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {listed.map((part) => (
                            <TableRow key={`${part.table}:${part.name}`}>
                              <TableCell className="py-2 font-mono">{part.name}</TableCell>
                              <TableCell className="px-2 py-2 font-mono text-muted-foreground">
                                {part.table}
                              </TableCell>
                              <TableCell className="numeric px-2 py-2 text-right">
                                {compact(part.rows)}
                              </TableCell>
                              <TableCell className="numeric px-2 py-2 text-right">
                                {bytes(part.bytes)}
                              </TableCell>
                              <TableCell
                                className="numeric px-2 py-2 text-right text-muted-foreground max-sm:hidden"
                                title="How many rounds of merging produced this part"
                              >
                                {part.level === 0 ? "not yet" : `${part.level}×`}
                              </TableCell>
                              <TableCell className="py-2 max-md:hidden">
                                <span className="flex items-center gap-2.5">
                                  <Tag>{part.type}</Tag>
                                  <Tag mono>{part.disk}</Tag>
                                  {!part.active && <Tag tone="warning">inactive</Tag>}
                                </span>
                              </TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    </div>
                  )}
                </Disclosure>
              )}
            </div>
          )
        }}
      </ViewRead>
    </Panel>
  )
}

/**
 * The server's background work on its parts: the merges running now, with
 * how far along each is, and the mutations asked of it — an `ALTER … UPDATE`
 * or `DELETE` rewrites parts one at a time, and one that cannot finish stays
 * here with the reason it failed.
 */
export function MergesView() {
  const { id } = useDatabase()
  const merges = usePoll((signal) => readChMerges(id, signal), 3000, [id])
  const mutations = usePoll((signal) => readChMutations(id, signal), 15_000, [id])
  return (
    <div className="space-y-8">
      <Panel plain aria-label="Merges">
        <PanelHeader title="Merges running" actions={<Stale poll={merges} />} />
        <ViewRead poll={merges} what="the merges" skeleton={<LoadingPanel plain rows={3} />}>
          {(data) => (
            <PanelBody flush className="animate-rise group-data-[plain]/panel:-mx-4">
              {data.merges.length === 0 ? (
                <EmptyNote className="px-4">
                  No merge is running. The server merges parts in the background as inserts arrive.
                </EmptyNote>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead>Table</TableHead>
                      <TableHead className="px-2">Progress</TableHead>
                      <TableHead className="px-2 text-right">For</TableHead>
                      <TableHead className="px-2 text-right">Parts</TableHead>
                      <TableHead className="px-2 text-right max-sm:hidden">Size</TableHead>
                      <TableHead className="text-right max-md:hidden">Memory</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {data.merges.map((merge) => (
                      <TableRow key={`${merge.table}:${merge.resultPart}`}>
                        <TableCell className="py-2">
                          <span className="flex items-center gap-2">
                            <span className="font-mono">{merge.table}</span>
                            {merge.isMutation && <Tag>mutation</Tag>}
                          </span>
                          <span className="block font-mono text-hint text-muted-foreground">
                            into {merge.resultPart}
                          </span>
                        </TableCell>
                        <TableCell className="px-2 py-2">
                          <span className="flex w-36 items-center gap-2">
                            <Meter
                              value={merge.progress * 100}
                              size="thin"
                              label={`Merge of ${merge.table}`}
                              className="flex-1"
                            />
                            <span className="numeric text-hint text-muted-foreground">
                              {Math.round(merge.progress * 100)}%
                            </span>
                          </span>
                        </TableCell>
                        <TableCell className="numeric px-2 py-2 text-right">
                          {duration(merge.elapsed)}
                        </TableCell>
                        <TableCell className="numeric px-2 py-2 text-right">
                          {merge.parts.toLocaleString()}
                        </TableCell>
                        <TableCell className="numeric px-2 py-2 text-right max-sm:hidden">
                          {bytes(merge.bytes)}
                        </TableCell>
                        <TableCell className="numeric py-2 text-right text-muted-foreground max-md:hidden">
                          {bytes(merge.memory)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </PanelBody>
          )}
        </ViewRead>
      </Panel>

      <Panel plain aria-label="Mutations">
        <PanelHeader title="Mutations" actions={<Stale poll={mutations} />} />
        <ViewRead poll={mutations} what="the mutations" skeleton={<LoadingPanel plain rows={3} />}>
          {(data) => (
            <PanelBody flush className="animate-rise group-data-[plain]/panel:-mx-4">
              {data.mutations.length === 0 ? (
                <EmptyNote className="px-4">
                  No mutation has been asked of this database. An ALTER TABLE … UPDATE or DELETE
                  appears here while it rewrites parts.
                </EmptyNote>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead>Table</TableHead>
                      <TableHead className="px-2">State</TableHead>
                      <TableHead className="px-2">Command</TableHead>
                      <TableHead className="max-sm:hidden">Asked</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {data.mutations.map((mutation) => (
                      <TableRow key={`${mutation.table}:${mutation.id}`}>
                        <TableCell className="py-2 font-mono">{mutation.table}</TableCell>
                        <TableCell className="px-2 py-2">
                          <Status
                            tone={
                              mutation.failReason ? "danger" : mutation.done ? "running" : "notice"
                            }
                            label={
                              mutation.failReason
                                ? "Failing"
                                : mutation.done
                                  ? "Done"
                                  : `${mutation.partsToDo.toLocaleString()} parts to do`
                            }
                          />
                        </TableCell>
                        <TableCell className="w-full max-w-0 px-2 py-2">
                          <StatementLine text={mutation.command} />
                          {mutation.failReason && (
                            <span
                              className={cn("block truncate text-hint text-destructive")}
                              title={mutation.failReason}
                            >
                              {mutation.failReason}
                            </span>
                          )}
                        </TableCell>
                        <TableCell
                          className="py-2 text-muted-foreground max-sm:hidden"
                          title={timestamp(mutation.created)}
                        >
                          {mutation.created ? relativeTime(mutation.created) : <NoFigure />}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </PanelBody>
          )}
        </ViewRead>
      </Panel>
    </div>
  )
}

/** How many of a kind of object, in words. */
function holds(objects: Record<string, number>): string {
  return (
    (["table", "index", "view", "trigger"] as const)
      .filter((kind) => objects[kind])
      .map(
        (kind) =>
          `${objects[kind]} ${objects[kind] === 1 ? kind : kind === "index" ? "indexes" : `${kind}s`}`,
      )
      .join(" · ") || "nothing yet"
  )
}

/**
 * A file-based database has no server to watch: what there is to know is the
 * file — where it is, what it weighs, how it is paged and journalled — and
 * what can be done for it is the engine's own housekeeping, run on the whole
 * file: gather statistics, check it for damage, fold the write-ahead log back
 * in, rebuild it to hand free pages back.
 */
export function FileView() {
  const { id, engine } = useDatabase()
  const file = usePoll((signal) => readSqliteFile(id, signal), 60_000, [id])
  const maintenance = useMaintenance(file.refresh)
  const verbs = maintenance.forTarget({})
  return (
    <div className="grid items-start gap-8 lg:grid-cols-2 [&>*]:min-w-0">
      {maintenance.dialogs}
      <Panel plain aria-label="The file">
        <PanelHeader title="The file" actions={<Stale poll={file} />} />
        <ViewRead poll={file} what="the file" skeleton={<LoadingPanel plain rows={8} />}>
          {(data) => (
            <PanelBody className="animate-rise space-y-5">
              <DetailList>
                <Detail label="Path" className="font-mono wrap-anywhere">
                  {data.path}
                </Detail>
                <Detail label="Size">{bytes(data.fileBytes)}</Detail>
                <Detail label="Write-ahead log">
                  {data.walBytes > 0 ? bytes(data.walBytes) : "none"}
                </Detail>
                {data.modified && (
                  <Detail label="Last written">
                    <span title={timestamp(data.modified)}>{relativeTime(data.modified)}</span>
                  </Detail>
                )}
                <Detail label="Pages">
                  {data.pageCount.toLocaleString()} of {bytes(data.pageSize, 0)}
                </Detail>
                <Detail label="Free pages">
                  {data.freelistPages.toLocaleString()}
                  {data.reclaimableBytes > 0 && (
                    <span className="text-muted-foreground">
                      {" "}
                      · {bytes(data.reclaimableBytes)} a vacuum would return
                    </span>
                  )}
                </Detail>
                <Detail label="Holds">{holds(data.objects)}</Detail>
              </DetailList>
              <section className="space-y-2">
                <p className="eyebrow">How it is kept</p>
                <DetailList>
                  <Detail label="Journal" className="font-mono">
                    {data.journalMode}
                  </Detail>
                  <Detail label="Synchronous" className="font-mono">
                    {data.synchronous}
                  </Detail>
                  <Detail label="Auto-vacuum" className="font-mono">
                    {data.autoVacuum}
                  </Detail>
                  <Detail label="Foreign keys">
                    {data.foreignKeys ? "enforced" : "not enforced"}
                  </Detail>
                  <Detail label="Text" className="font-mono">
                    {data.encoding}
                  </Detail>
                  <Detail label="Schema version">{data.schemaVersion.toLocaleString()}</Detail>
                  <Detail label="User version">{data.userVersion.toLocaleString()}</Detail>
                  <Detail label={engine.label}>{data.version}</Detail>
                </DetailList>
              </section>
              {data.attached.length > 0 && (
                <section className="space-y-2">
                  <p className="eyebrow">Attached</p>
                  <DetailList>
                    {data.attached.map((entry) => (
                      <Detail
                        key={entry.name}
                        label={<span className="font-mono">{entry.name}</span>}
                        className="font-mono wrap-anywhere"
                      >
                        {entry.file || "in memory"}
                      </Detail>
                    ))}
                  </DetailList>
                </section>
              )}
              {data.compileOptions.length > 0 && (
                <Disclosure quiet summary={<>Built with · {data.compileOptions.length} options</>}>
                  <p className="flex flex-wrap gap-1.5">
                    {data.compileOptions.map((option) => (
                      <Tag key={option} mono>
                        {option}
                      </Tag>
                    ))}
                  </p>
                </Disclosure>
              )}
            </PanelBody>
          )}
        </ViewRead>
      </Panel>

      <Panel plain aria-label="Maintenance">
        <PanelHeader title="Check and maintain" />
        <PanelBody>
          <ViewRead
            poll={maintenance.list}
            what="what can be run on the file"
            skeleton={<LoadingPanel plain rows={4} />}
          >
            {() =>
              verbs.length === 0 ? (
                <EmptyNote>
                  Nothing can be run on this file from here: this role may not, or the connection is
                  protected and the engine lists no check that changes nothing.
                </EmptyNote>
              ) : (
                <MaintenanceList verbs={verbs} maintenance={maintenance} on="the whole file" />
              )
            }
          </ViewRead>
        </PanelBody>
      </Panel>
    </div>
  )
}

"use client"

import { useMemo, useState } from "react"
import { Copy } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { bytes } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { FormNote } from "@/components/form"
import { RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, LoadingPanel } from "@/components/state"
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
import { VerbMenu, type Verb } from "@/components/verbs"
import { compact } from "@/components/database/home/readings"
import { STORAGE_LIMIT, readIndexStats } from "@/components/database/ops/performance-api"
import { useMaintenance } from "@/components/database/ops/performance-maintenance"
import {
  NoFigure,
  NotAvailable,
  Notes,
  ObjectName,
  ScopeChips,
  Stale,
  ViewRead,
} from "@/components/database/ops/performance-parts"
import {
  countFlags,
  flaggedBytes,
  indexFlags,
  objectParam,
  qualified,
  schemasOf,
  type IndexFlag,
} from "@/components/database/ops/performance-storage"
import type { DbIndexStat } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

const FLAGS: { id: IndexFlag; chip: string; words: (count: number) => string }[] = [
  {
    id: "invalid",
    chip: "Invalid",
    words: (n) => `${n === 1 ? "is" : "are"} invalid, and used by no query`,
  },
  {
    id: "duplicate",
    chip: "Duplicate",
    words: (n) => `${n === 1 ? "repeats" : "repeat"} another index exactly`,
  },
  {
    id: "covered",
    chip: "Covered",
    words: (n) => `${n === 1 ? "is" : "are"} the leading columns of a wider index`,
  },
  {
    id: "unused",
    chip: "Unused",
    words: (n) => `${n === 1 ? "has" : "have"} never been scanned`,
  },
]

const isFlag = (value: string): value is IndexFlag => FLAGS.some((flag) => flag.id === value)

/**
 * Every index, by what it weighs and whether it earns it: its size, how often
 * it has been scanned, and the four reasons one is dead weight — it was never
 * used, it repeats another, a wider one already covers it, or it is invalid
 * and no query can use it.
 *
 * An index costs every write to its table, so the ones that give nothing back
 * are what this view is for: a chip per reason narrows the list to them and
 * says how much disk they hold. An index is called unused only where the
 * engine counts scans; where it does not, the engine's own note says so and
 * no index is accused.
 */
export function IndexesView() {
  const { id, engine, param, select } = useDatabase()
  const stats = usePoll((signal) => readIndexStats(id, signal), 60_000, [id])
  const maintenance = useMaintenance(stats.refresh)
  const [filter, setFilter] = useState("")
  const scope = param("scope")
  const asked = param("flag")
  const flag = isFlag(asked) ? asked : null

  const all = stats.data?.indexes
  const schemas = useMemo(() => schemasOf(all ?? []), [all])
  const inScope = useMemo(
    () => (all ?? []).filter((index) => !scope || index.schema === scope),
    [all, scope],
  )
  const counts = useMemo(() => countFlags(inScope), [inScope])
  const largest = useMemo(
    () => inScope.reduce((most, index) => Math.max(most, index.bytes), 0),
    [inScope],
  )
  const counted = inScope.some((index) => index.scans >= 0)
  const rows = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return inScope.filter(
      (index) =>
        (!flag || indexFlags(index).includes(flag)) &&
        (!needle ||
          `${index.name} ${qualified(index.schema, index.table)} ${index.columns.join(" ")}`
            .toLowerCase()
            .includes(needle)),
    )
  }, [inScope, flag, filter])

  const verbsFor = (index: DbIndexStat): Verb[] => [
    ...maintenance.verbsFor({ schema: index.schema, table: index.table, index: index.name }),
    ...(index.definition
      ? [
          {
            key: "copy",
            label: "Copy definition",
            icon: Copy,
            run: () => void copyText(index.definition ?? "", "Definition copied"),
          },
        ]
      : []),
  ]

  return (
    <Panel plain aria-label="Indexes">
      {maintenance.dialogs}
      <PanelHeader
        title="Indexes"
        actions={
          <>
            <Stale poll={stats} />
            <SearchInput
              dense
              aria-label="Filter the indexes"
              placeholder="Filter by name, table or column"
              value={filter}
              containerClassName="sm:w-60"
              onChange={(event) => setFilter(event.target.value)}
            />
          </>
        }
      />
      <ViewRead poll={stats} what="the indexes" skeleton={<LoadingPanel plain rows={8} />}>
        {(data) =>
          !data.supported ? (
            <div className="pt-4">
              <NotAvailable title="The index statistics cannot be read" reason={data.reason} />
            </div>
          ) : (
            <div className="animate-rise space-y-3 pt-3">
              <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2">
                <ChipStrip role="group" aria-label="Indexes by what is wrong with them">
                  <FilterChip selected={!flag} onClick={() => select({ flag: null })}>
                    All
                    <ChipCount>{inScope.length.toLocaleString()}</ChipCount>
                  </FilterChip>
                  {FLAGS.filter((entry) => counts[entry.id] > 0 || entry.id === flag).map(
                    (entry) => (
                      <FilterChip
                        key={entry.id}
                        selected={flag === entry.id}
                        onClick={() => select({ flag: flag === entry.id ? null : entry.id })}
                      >
                        {entry.chip}
                        <ChipCount>{counts[entry.id].toLocaleString()}</ChipCount>
                      </FilterChip>
                    ),
                  )}
                </ChipStrip>
                <div className="min-w-0 sm:ml-auto">
                  <ScopeChips
                    schemas={schemas}
                    scope={scope}
                    onScope={(schema) => select({ scope: schema || null })}
                    noun={engine.nouns.containers}
                  />
                </div>
              </div>
              {flag && counts[flag] > 0 && (
                <p className="text-body">
                  <span className="numeric font-medium">
                    {counts[flag].toLocaleString()} {counts[flag] === 1 ? "index" : "indexes"}
                  </span>{" "}
                  <span className="text-muted-foreground">
                    {FLAGS.find((entry) => entry.id === flag)?.words(counts[flag])}, holding{" "}
                    {bytes(flaggedBytes(inScope, flag))}.
                  </span>
                </p>
              )}
              <PanelBody flush className="group-data-[plain]/panel:-mx-4">
                {data.indexes.length === 0 ? (
                  <EmptyNote className="px-4">
                    {data.notes?.length
                      ? "The engine lists no index here."
                      : `No ${engine.nouns.object} of this database has an index.`}
                  </EmptyNote>
                ) : rows.length === 0 ? (
                  <EmptyNote className="px-4">No index matches.</EmptyNote>
                ) : (
                  <Table>
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        <TableHead>Index</TableHead>
                        <TableHead className="px-2 max-md:hidden">Columns</TableHead>
                        <TableHead className="px-2 text-right">Size</TableHead>
                        {counted && <TableHead className="px-2 text-right">Scans</TableHead>}
                        <TableHead className="px-2 max-sm:hidden">Notes</TableHead>
                        <TableHead>
                          <span className="sr-only">Actions</span>
                        </TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((index) => {
                        const verbs = verbsFor(index)
                        return (
                          <TableRow key={`${index.schema}.${index.table}.${index.name}`}>
                            <TableCell className="max-w-72 py-2">
                              <span className="block truncate font-mono" title={index.name}>
                                {index.name}
                              </span>
                              <span className="flex min-w-0 items-center gap-1 text-hint text-muted-foreground">
                                on
                                <RowLink
                                  mono
                                  className="text-hint font-normal"
                                  onClick={() =>
                                    select({
                                      view: "tables",
                                      flag: null,
                                      object: objectParam(index.schema, index.table),
                                    })
                                  }
                                >
                                  <span className="sr-only">Open the table </span>
                                  <ObjectName
                                    schema={schemas.length > 1 ? index.schema : undefined}
                                    name={index.table}
                                  />
                                </RowLink>
                              </span>
                            </TableCell>
                            <TableCell className="px-2 py-2 whitespace-normal max-md:hidden">
                              <span className="flex flex-wrap items-center gap-1">
                                {index.columns.map((column) => (
                                  <Tag key={column} mono className="max-w-48 truncate">
                                    {column}
                                  </Tag>
                                ))}
                                {index.method && (
                                  <span className="text-hint text-muted-foreground">
                                    {index.method}
                                  </span>
                                )}
                              </span>
                            </TableCell>
                            <TableCell className="px-2 py-2">
                              <span className="flex min-w-28 items-center justify-end gap-2.5">
                                <span
                                  aria-hidden
                                  className="relative h-1 min-w-8 flex-1 overflow-hidden rounded-full bg-meter-track"
                                >
                                  <span
                                    className="absolute inset-y-0 left-0 rounded-full bg-(--chart-2)"
                                    style={{
                                      width: `${largest > 0 ? Math.max((index.bytes / largest) * 100, 2) : 0}%`,
                                    }}
                                  />
                                </span>
                                <span className="numeric w-16 shrink-0 text-right">
                                  {bytes(index.bytes)}
                                </span>
                              </span>
                            </TableCell>
                            {counted && (
                              <TableCell className="numeric px-2 py-2 text-right">
                                {index.scans < 0 ? <NoFigure /> : compact(index.scans)}
                              </TableCell>
                            )}
                            <TableCell className="px-2 py-2 whitespace-normal max-sm:hidden">
                              <IndexTags index={index} />
                            </TableCell>
                            <TableCell className="py-1 text-right">
                              {verbs.length > 0 && (
                                <VerbMenu verbs={verbs} label={`Actions for index ${index.name}`} />
                              )}
                            </TableCell>
                          </TableRow>
                        )
                      })}
                    </TableBody>
                  </Table>
                )}
              </PanelBody>
              {data.truncated && (
                <FormNote>
                  The {STORAGE_LIMIT} largest are listed; this database holds more indexes than
                  that, and a copy smaller than the cut can go unmarked.
                </FormNote>
              )}
              <Notes notes={data.notes} />
            </div>
          )
        }
      </ViewRead>
    </Panel>
  )
}

/** What an index is, and what is wrong with it, as the words at its row's edge. */
function IndexTags({ index }: { index: DbIndexStat }) {
  return (
    <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
      {index.primary ? <Tag>primary key</Tag> : index.unique && <Tag>unique</Tag>}
      {!index.valid && <Tag tone="danger">invalid</Tag>}
      {index.duplicateOf && (
        <Tag tone="warning" title={`An identical index: ${index.duplicateOf}`}>
          duplicate of{" "}
          <span className="font-mono tracking-normal normal-case">{index.duplicateOf}</span>
        </Tag>
      )}
      {index.coveredBy && (
        <Tag tone="warning" title={`A wider index starts with these columns: ${index.coveredBy}`}>
          covered by{" "}
          <span className="font-mono tracking-normal normal-case">{index.coveredBy}</span>
        </Tag>
      )}
      {index.unused && <Tag tone="warning">unused</Tag>}
    </span>
  )
}

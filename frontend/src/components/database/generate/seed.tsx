"use client"

import Link from "next/link"
import { useEffect, useMemo, useState } from "react"
import {
  ChevronDown,
  CheckCircle,
  Information,
  Plus,
  RefreshClockwise,
  Table as TableIcon,
  Warning,
} from "@/components/icons"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { FormNote, Statement } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, Notice } from "@/components/state"
import { LoadingRows, Sweep } from "@/components/loading"
import { ViewTabs } from "@/components/tabs"
import { TabsContent } from "@/components/ui/tabs"
import { Tag } from "@/components/tag"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { SqlReview } from "@/components/database/kit/sql-review"
import { ReadError } from "@/components/database/kit/read-error"
import { useDatabase } from "@/components/database/shell/database-context"
import { TablePicker } from "@/components/database/generate/table-picker"
import {
  DEFAULT_SEED_ROWS,
  MAX_SEED_ROWS,
  MAX_SEED_TABLES,
  chooseGenerator,
  choiceGroups,
  newSeed,
  planLine,
  pruneChoices,
  readSeedPlan,
  readSeedReport,
  reportLine,
  requestedChoices,
  reportTitle,
  rowsFrom,
  seedKey,
  seedRequest,
  seedTargets,
  tableLabel,
  type SeedChoices,
  type SeedColumn,
  type SeedPlan,
  type SeedReport,
  type SeedRows,
  type SeedTable,
  type SeedTarget,
} from "@/components/database/generate/seed-model"

const NO_NAMES: string[] = []
const NO_ROWS: SeedRows = {}
const NO_CHOICES: SeedChoices = {}

/** How long the page waits after a change before it asks for the plan again. */
const SETTLE_MS = 450

/**
 * Rows of mock data for tables that exist, beside the code the other
 * generators write from the same catalogue.
 *
 * The server does the work: it reads each table, gives every column a
 * generator from its name and type (an email column gets addresses, a column
 * held to a list gets the list), orders the tables so a foreign key always has
 * its parent's rows to point at, and shows the first rows of each. What is
 * here is the reader's side of that — which tables, how many rows, a column's
 * generator changed — and the one command that writes them.
 *
 * Nothing is asked until the reader asks: choosing Seed data, or Preview. From
 * then on a change asks again by itself. The preview is a read and is offered
 * to every role and on a protected connection; the write is not, and says why.
 * The rows a preview shows are the rows the write writes, by the seed number
 * both carry — and once written the number moves on, so the next seed is new
 * rows rather than the same ones refused as duplicates.
 */
export function SeedPanel({
  targets,
  several,
  loading,
  scope,
  browse,
  browsing,
  generators,
  armedAtStart,
}: {
  /** Every table of the schemas read. */
  targets: SeedTarget[]
  several: boolean
  loading: boolean
  /** The schema chips, where there is more than one schema to read. */
  scope?: React.ReactNode
  /** The control that unfolds the generators where they cannot stand beside. */
  browse?: React.ReactNode
  browsing: boolean
  generators: React.ReactNode
  /** Chosen on this visit: choosing it is asking for its plan. */
  armedAtStart: boolean
}) {
  const { id, conn, engine, readOnly } = useDatabase()
  const { can } = useAuth()

  const names = useMemo(() => targets.map((t) => tableLabel(t, several)), [targets, several])
  const [picked, setPicked] = useSessionState<string[]>(`databases.${id}.seed.tables`, NO_NAMES)
  const tables = useMemo(() => picked.filter((name) => names.includes(name)), [picked, names])
  const chosen = useMemo(
    () => seedTargets(targets, tables, (t) => tableLabel(t, several)),
    [targets, tables, several],
  )

  const [rowsText, setRowsText] = useSessionState(
    `databases.${id}.seed.rows`,
    String(DEFAULT_SEED_ROWS),
  )
  const rows = rowsFrom(rowsText)
  const [perTable, setPerTable] = useSessionState<SeedRows>(`databases.${id}.seed.each`, NO_ROWS)
  const [choices, setChoices] = useSessionState<SeedChoices>(
    `databases.${id}.seed.choices`,
    NO_CHOICES,
  )
  const [firstSeed] = useState(() => newSeed())
  const [seed, setSeed] = useSessionState(`databases.${id}.seed.number`, firstSeed)
  // A table's own count as typed where it is not one the server takes: it
  // holds the plan, and the write, until it is.
  const [badRows, setBadRows] = useState<Record<string, string>>({})
  const badTable = chosen.find((t) => Object.hasOwn(badRows, seedKey(t.schema, t.name)))

  /* ------------------------------------------------------------- planning */

  const problem =
    rows === undefined
      ? `Rows: a whole number from 1 to ${MAX_SEED_ROWS.toLocaleString("en-US")}.`
      : badTable
        ? `Rows for ${tableLabel(badTable, several)}: a whole number from 1 to ${MAX_SEED_ROWS.toLocaleString("en-US")}.`
        : chosen.length > MAX_SEED_TABLES
          ? `A seed fills at most ${MAX_SEED_TABLES} tables at once; ${chosen.length} are chosen.`
          : undefined
  const wanted =
    rows !== undefined && !problem && chosen.length > 0
      ? JSON.stringify(seedRequest({ seed, targets: chosen, rows, perTable, choices }))
      : ""
  const [armed, setArmed] = useState(armedAtStart)
  // Chosen again while it is open — a card remembered from the last visit,
  // pressed — is asking all the same.
  if (armedAtStart && !armed) setArmed(true)
  const [asked, setAsked] = useState<{ key: string; nonce: number }>({ key: "", nonce: 0 })
  useEffect(() => {
    if (!armed || !wanted || wanted === asked.key) return
    const timer = setTimeout(() => setAsked((held) => ({ ...held, key: wanted })), SETTLE_MS)
    return () => clearTimeout(timer)
  }, [armed, wanted, asked.key])
  // A plan read past (the reader changed something meanwhile) is let go,
  // and the server stops counting for it.
  const planning = usePoll(
    async (signal) => ({
      plan: readSeedPlan(
        await post<unknown>(`/databases/${id}/seed/plan`, JSON.parse(asked.key), { signal }),
      ),
      asked: requestedChoices(asked.key),
    }),
    0,
    [id, asked.key, asked.nonce],
    { enabled: asked.key !== "" },
  )
  const answered = planning.data
  useEffect(() => {
    if (answered) setChoices((held) => pruneChoices(held, answered.asked, answered.plan))
  }, [answered, setChoices])
  // The plan on screen stays while the next one is read.
  const [kept, setKept] = useState<SeedPlan>()
  if (answered && answered.plan !== kept) setKept(answered.plan)
  const plan = answered?.plan ?? (planning.error ? undefined : kept)
  const reading = planning.loading || (armed && wanted !== "" && wanted !== asked.key)
  // What a write sends is what the preview on screen shows, and nothing newer.
  const current = Boolean(plan) && !reading && asked.key === wanted

  const preview = () => {
    if (!wanted) return
    setArmed(true)
    setAsked((held) => ({ key: wanted, nonce: held.nonce + 1 }))
  }

  /* -------------------------------------------------------------- writing */

  const [reviewing, setReviewing] = useState(false)
  const [writing, setWriting] = useState(false)
  const [report, setReport] = useState<SeedReport>()
  const mayWrite = can("service.control") && !readOnly
  const writeWhy = readOnly
    ? `${conn.name} is protected: nothing can be written to it from the dashboard.`
    : !can("service.control")
      ? "Inserting rows needs a role that may change data."
      : undefined
  const environment = conn.environment?.trim()
  const live = Boolean(environment && /prod/i.test(environment))
  const write = async () => {
    setWriting(true)
    try {
      const done = readSeedReport(
        await post<unknown>(`/databases/${id}/seed`, JSON.parse(asked.key)),
      )
      setReport(done)
      setReviewing(false)
    } catch (err) {
      notify.error("The rows were not inserted", err)
    } finally {
      setWriting(false)
      // The same number again would make the same rows: refused by a unique
      // key, or — where some of them went in without a transaction to take
      // them back — written a second time. The next preview is of new ones.
      setSeed(newSeed())
    }
  }

  /* --------------------------------------------------------------- render */

  const total = plan?.rows ?? 0
  const writable = plan?.tables.filter((t) => !t.problem && t.rows > 0) ?? []

  return (
    <Panel plain>
      <PanelHeader
        title={
          <span className="flex min-w-0 items-center gap-2.5">
            <span className="min-w-0 truncate">Seed data</span>
            <Tag className="shrink-0">Test rows</Tag>
          </span>
        }
        actions={
          <>
            {browse}
            {!browsing && (
              <>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setSeed(newSeed())
                    setArmed(true)
                  }}
                  title={`Seed ${seed}: the same number makes the same rows`}
                >
                  <RefreshClockwise />
                  New values
                </Button>
                {!plan ? (
                  <Button size="sm" onClick={preview} pending={planning.loading} disabled={!wanted}>
                    <TableIcon />
                    Preview
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    onClick={() => setReviewing(true)}
                    disabled={!mayWrite || !current || total === 0}
                    title={writeWhy}
                  >
                    <Plus />
                    Insert {total.toLocaleString("en-US")} {total === 1 ? "row" : "rows"}
                  </Button>
                )}
              </>
            )}
          </>
        }
      />
      <PanelBody className="space-y-2">
        {browsing ? (
          generators
        ) : (
          <>
            {/* Said before the controls, because a card beside the code
                generators reads as one more of them until it says it writes. */}
            <Notice
              tone={live ? "warning" : "default"}
              icon={live ? Warning : Information}
              title={`Inserts made-up test rows into ${conn.name}${environment ? ` (${environment})` : ""}`}
            >
              Values suit each column&apos;s name and type, a table is filled after the ones it
              references so its foreign keys point at real rows, and the rows already there stay.
              Nothing is inserted until you review the statements and confirm.
            </Notice>
            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
              {scope}
              <TablePicker
                names={names}
                picked={tables}
                loading={loading}
                onChange={setPicked}
                what="filled"
              />
              <InputGroup className="h-10 w-32 shrink-0 sm:h-7">
                <InputGroupInput
                  inputMode="numeric"
                  value={rowsText}
                  aria-label="Rows per table"
                  aria-invalid={rows === undefined}
                  className="numeric text-xs"
                  onChange={(event) => setRowsText(event.target.value)}
                />
                <InputGroupAddon align="inline-end">
                  <InputGroupText className="text-xs">rows each</InputGroupText>
                </InputGroupAddon>
              </InputGroup>
              {plan && (
                <span className="numeric text-hint text-muted-foreground">{planLine(plan)}</span>
              )}
            </div>
            {report && (
              <Written report={report} several={several} onDismiss={() => setReport(undefined)} />
            )}
            <SeedOutput
              plan={plan}
              error={planning.error}
              onRetry={preview}
              reading={reading}
              problem={problem}
              empty={!loading && targets.length === 0}
              several={several}
              rows={rows ?? DEFAULT_SEED_ROWS}
              perTable={perTable}
              badRows={badRows}
              onRows={(key, value) => {
                setBadRows((held) => {
                  const next = { ...held }
                  if (typeof value === "string") next[key] = value
                  else delete next[key]
                  return next
                })
                if (typeof value === "string") return
                setPerTable((held) => {
                  const next = { ...held }
                  if (value === rows) delete next[key]
                  else next[key] = value
                  return next
                })
              }}
              onChoose={(table, column, generator) =>
                setChoices((held) =>
                  chooseGenerator(
                    held,
                    seedKey(table.schema, table.name),
                    column.name,
                    generator,
                    column.inferred,
                  ),
                )
              }
            />
          </>
        )}
      </PanelBody>
      <SqlReview
        open={reviewing && plan !== undefined}
        onOpenChange={(open) => !writing && setReviewing(open)}
        title={`Insert ${total.toLocaleString("en-US")} rows into ${writable.length === 1 ? "1 table" : `${writable.length} tables`}`}
        statements={writable.map((t) => t.statement).filter(Boolean)}
        command={`Insert ${total.toLocaleString("en-US")} ${total === 1 ? "row" : "rows"}`}
        onRun={() => void write()}
        pending={writing}
        note={
          plan?.atomic === false
            ? `${engine.label} has no transaction: each table is inserted on its own, and one inserted before a failure stays.`
            : "One transaction: if any of it fails, nothing is inserted."
        }
      >
        <ul aria-label="What is inserted" className="space-y-1 text-body">
          {writable.map((t, index) => (
            <li key={seedKey(t.schema, t.name)} className="flex min-w-0 items-baseline gap-2">
              <span className="numeric w-5 shrink-0 text-right text-hint text-muted-foreground">
                {index + 1}
              </span>
              <span className="min-w-0 truncate font-mono text-xs">{tableLabel(t, several)}</span>
              <span className="numeric ml-auto shrink-0 text-xs text-muted-foreground">
                {t.rows.toLocaleString("en-US")} {t.rows === 1 ? "row" : "rows"}
              </span>
            </li>
          ))}
        </ul>
      </SqlReview>
    </Panel>
  )
}

/** The plan: a tab for each table in the order they are written, and the chosen one's rows. */
function SeedOutput({
  plan,
  error,
  onRetry,
  reading,
  problem,
  empty,
  several,
  rows,
  perTable,
  badRows,
  onRows,
  onChoose,
}: {
  plan: SeedPlan | undefined
  error: Error | undefined
  onRetry: () => void
  reading: boolean
  problem: string | undefined
  empty: boolean
  several: boolean
  rows: number
  perTable: SeedRows
  badRows: Record<string, string>
  /** A count the server takes, or the text typed where it is not one. */
  onRows: (key: string, value: number | string) => void
  onChoose: (table: SeedTable, column: SeedColumn, generator: string) => void
}) {
  const [openKey, setOpenKey] = useState("")
  const tables = plan?.tables ?? []
  const at = Math.max(
    0,
    tables.findIndex((t) => seedKey(t.schema, t.name) === openKey),
  )
  const open = tables[at]

  if (error) return <ReadError error={error} onRetry={onRetry} />
  if (empty) {
    return (
      <EmptyState
        icon={TableIcon}
        title="No tables to fill"
        description="The schemas read hold no tables. A view takes no rows of its own."
      />
    )
  }
  if (!plan || !open) {
    return reading ? (
      <div role="status" aria-label="Reading the tables" className="space-y-3">
        <Sweep rounded className="h-0.5" />
        <LoadingRows rows={8} />
      </div>
    ) : (
      <EmptyState
        icon={TableIcon}
        title="Nothing planned yet"
        description={
          problem ??
          "Preview reads each table — what its columns hold, how many rows it has, the rows its foreign keys can point at — and shows the rows that would be inserted."
        }
      />
    )
  }

  const key = seedKey(open.schema, open.name)
  return (
    <section aria-label="The rows planned" className="min-w-0 animate-rise space-y-2">
      {problem && (
        <Notice tone="warning" icon={Warning} title="Not planned again">
          {problem} The rows shown are the ones from before this change.
        </Notice>
      )}
      {plan.warnings.length > 0 && <FormNote tone="warning">{plan.warnings.join(" ")}</FormNote>}
      {/* A tab for each table, in the order they are written; the rows are its panel. */}
      <ViewTabs
        label="Tables seeded"
        views={tables.map((t) => ({
          key: seedKey(t.schema, t.name),
          label: tableLabel(t, several),
          count: t.problem ? undefined : t.rows,
          dot: t.problem ? { tone: "warning" as const, label: "left out" } : undefined,
        }))}
        value={key}
        onValueChange={setOpenKey}
        size="sm"
        mono
      >
        <TabsContent
          value={key}
          className={cn("relative mt-2 space-y-3 transition-opacity", problem && "opacity-60")}
        >
          <TableFacts
            table={open}
            rows={Object.hasOwn(perTable, key) ? perTable[key] : rows}
            typed={Object.hasOwn(badRows, key) ? badRows[key] : undefined}
            onRows={(value) => onRows(key, value)}
          />
          {open.problem ? (
            <Notice tone="warning" icon={Warning} title="Left out of this seed">
              {open.problem}
            </Notice>
          ) : (
            <>
              {open.notes.length > 0 && (
                <ul aria-label="What the rows are written with" className="space-y-1">
                  {open.notes.map((note, index) => (
                    <li key={index}>
                      <FormNote>{note}</FormNote>
                    </li>
                  ))}
                </ul>
              )}
              <PreviewGrid
                table={open}
                onChoose={(column, generator) => onChoose(open, column, generator)}
              />
              <Statement label="Each row is inserted with" sql={open.statement} placeholder="" />
            </>
          )}
          {reading && (
            <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
              <BorderBeam size={120} duration={4} />
            </span>
          )}
        </TabsContent>
      </ViewTabs>
    </section>
  )
}

/**
 * What is true of the table open: where it falls in the order, and what it
 * holds now beside what it will — the count after is what says the rows are
 * added to the ones there rather than put in their place.
 */
function TableFacts({
  table,
  rows,
  typed,
  onRows,
}: {
  table: SeedTable
  rows: number
  /** What was typed, where it is not a count the server takes. */
  typed?: string
  onRows: (value: number | string) => void
}) {
  const text = typed ?? String(rows)
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 text-xs text-muted-foreground">
      <span>
        {table.after.length > 0 ? (
          <>
            Inserted after{" "}
            {table.after.map((name, index) => (
              <span key={name}>
                {index > 0 && (index === table.after.length - 1 ? " and " : ", ")}
                <span className="font-mono text-foreground">{name}</span>
              </span>
            ))}
            , which it references
          </>
        ) : (
          "References no other table being filled"
        )}
      </span>
      {table.existing >= 0 && (
        <span className="numeric">
          {table.existing.toLocaleString("en-US")} {table.existing === 1 ? "row" : "rows"} now
          {!table.problem && `, ${(table.existing + table.rows).toLocaleString("en-US")} after`}
        </span>
      )}
      {table.rows < table.asked && !table.problem && (
        <span className="numeric text-warning">
          {table.rows.toLocaleString("en-US")} of {table.asked.toLocaleString("en-US")} asked for
        </span>
      )}
      <InputGroup className="ml-auto h-10 w-28 shrink-0 sm:h-7">
        <InputGroupInput
          inputMode="numeric"
          value={text}
          aria-label={`Rows for ${table.name}`}
          aria-invalid={typed !== undefined}
          className="numeric text-xs"
          onChange={(event) => onRows(rowsFrom(event.target.value) ?? event.target.value)}
        />
        <InputGroupAddon align="inline-end">
          <InputGroupText className="text-xs">rows</InputGroupText>
        </InputGroupAddon>
      </InputGroup>
    </div>
  )
}

/**
 * The first rows, one column to a column of the table. Each column's head is
 * two lines: its name with its type, and what fills it — which is where the
 * reader changes that. Name, type and generator each on a line of their own
 * made a head taller than three rows of the preview under it. A value the
 * write decides stands as what decides it, set apart from a NULL the seed
 * writes.
 */
function PreviewGrid({
  table,
  onChoose,
}: {
  table: SeedTable
  onChoose: (column: SeedColumn, generator: string) => void
}) {
  return (
    <div className="overflow-hidden rounded-lg border border-hairline">
      <Table
        aria-label={`The first rows of ${table.name}`}
        containerClassName="max-h-[calc(100svh-26rem)] min-h-40"
      >
        <TableHeader className="sticky top-0 z-raised bg-card [&_tr]:border-b">
          <TableRow className="hover:bg-transparent">
            {table.columns.map((column) => (
              <TableHead key={column.name} className="h-auto px-3 pt-2 pb-1 align-top">
                <div className="flex max-w-64 min-w-32 flex-col">
                  <span className="flex min-w-0 items-baseline gap-1.5">
                    <span className="min-w-0 truncate font-mono text-xs text-foreground">
                      {column.name}
                    </span>
                    <span
                      className="min-w-6 shrink-[3] truncate font-mono text-micro font-normal"
                      title={column.type}
                    >
                      {column.type}
                    </span>
                    {column.unique && <Tag className="shrink-0">unique</Tag>}
                  </span>
                  <GeneratorPicker column={column} onChoose={(id) => onChoose(column, id)} />
                </div>
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {table.preview.map((row, index) => (
            <TableRow key={index}>
              {table.columns.map((column, at) => {
                const value = row[at]
                return (
                  <TableCell key={column.name} className="max-w-64 px-3 py-1.5">
                    {value !== null ? (
                      <span className="block truncate font-mono" title={value}>
                        {value}
                      </span>
                    ) : column.later ? (
                      <span className="block truncate text-muted-foreground italic">
                        {column.later}
                      </span>
                    ) : (
                      <span className="font-mono text-muted-foreground">NULL</span>
                    )}
                  </TableCell>
                )
              })}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

/** What fills a column, and the menu of what else can. */
function GeneratorPicker({
  column,
  onChoose,
}: {
  column: SeedColumn
  onChoose: (id: string) => void
}) {
  const reference = column.reference
    ? `${column.reference.table}.${column.reference.columns.join(", ")}`
    : ""
  // The fixed one takes the menu's height, so every column's generator sits on one line.
  if (column.choices.length <= 1) {
    return (
      <span
        className="flex h-6 min-w-0 items-center text-hint font-normal text-muted-foreground"
        title={reference || undefined}
      >
        <span className="truncate">{column.label}</span>
      </span>
    )
  }
  const groups = choiceGroups(column.choices)
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="xs"
          variant="ghost"
          aria-label={`${column.name} is filled with ${column.label}`}
          className="-ml-1.5 h-6 max-w-full justify-start gap-1 px-1.5 text-hint font-normal text-muted-foreground"
        >
          <span className="truncate">{column.label}</span>
          <ChevronDown className="size-3 shrink-0" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 w-56 overflow-y-auto">
        <DropdownMenuRadioGroup value={column.generator} onValueChange={onChoose}>
          {groups.map((group, index) => (
            <div key={group.group}>
              {index > 0 && <DropdownMenuSeparator />}
              <DropdownMenuLabel>{group.group}</DropdownMenuLabel>
              {group.choices.map((choice) => (
                <DropdownMenuRadioItem key={choice.id} value={choice.id}>
                  {choice.label}
                </DropdownMenuRadioItem>
              ))}
            </div>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** What the last write did, table by table, until it is put away or another is made. */
function Written({
  report,
  several,
  onDismiss,
}: {
  report: SeedReport
  several: boolean
  onDismiss: () => void
}) {
  const { href } = useDatabase()
  const refused = report.skipped > 0 || report.tables.some((t) => t.problem)
  return (
    <Notice
      tone={report.inserted === 0 ? "warning" : refused ? "warning" : "success"}
      icon={refused || report.inserted === 0 ? Warning : CheckCircle}
      title={
        <span className="flex min-w-0 items-baseline gap-3">
          <span className="min-w-0 flex-1">{reportTitle(report)}</span>
          <Button size="xs" variant="ghost" onClick={onDismiss}>
            Dismiss
          </Button>
        </span>
      }
    >
      <ul aria-label="What was written" className="space-y-1">
        {report.tables.map((t) => (
          <li key={seedKey(t.schema, t.name)} className="min-w-0">
            <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
              {t.inserted > 0 ? (
                <Link
                  href={href("data", { schema: t.schema, table: t.name })}
                  className="font-mono text-foreground underline-offset-2 hover:underline"
                >
                  {tableLabel(t, several)}
                </Link>
              ) : (
                <span className="font-mono text-foreground">{tableLabel(t, several)}</span>
              )}
              <span className="numeric">{reportLine(t)}</span>
            </span>
            {(t.problem ?? t.errors[0]?.message) && (
              <span className="block truncate" title={t.problem ?? t.errors[0]?.message}>
                {t.problem ?? `Row ${t.errors[0].row}: ${t.errors[0].message}`}
              </span>
            )}
          </li>
        ))}
      </ul>
    </Notice>
  )
}

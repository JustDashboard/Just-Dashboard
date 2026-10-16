"use client"

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Plus, SidebarLeftOpen, Table } from "@/components/icons"
import { usePanelSize } from "@/lib/panel-size"
import { cn } from "@/lib/utils"
import { useMemoryState, useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { IconAction } from "@/components/icon-action"
import { ResizeHandle } from "@/components/resize-handle"
import { EmptyState, ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { EMPTY_CHANGE_STATE, useChangeSet, type ChangeSetState } from "@/components/database/grid"
import { EngineMark, SectionFrame } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  SETTLED,
  addressKey,
  addressOf,
  addressParams,
  landed,
  wrote,
  type Flight,
} from "@/components/database/data/address"
import { focusAfterDialog } from "@/components/database/data/focus"
import { LeaveGuard, useLinkGuard, useUnloadGuard } from "@/components/database/data/guard"
import { ImportDialog } from "@/components/database/data/import-dialog"
import { SchemaLanding } from "@/components/database/data/landing"
import { ROW_GROUPS } from "@/components/database/data/kinds"
import { INSPECTOR, RAIL, arrange } from "@/components/database/data/panes"
import { TableRail, tableKey } from "@/components/database/data/rail"
import { useTableVerbs } from "@/components/database/data/table-verbs"
import type { DbCatalogObject } from "@/components/database/data/types"
import { useExport } from "@/components/database/data/use-export"
import { useCatalog, useTableDetail } from "@/components/database/data/use-table"
import { readView, sameRows, viewParams, type ViewState } from "@/components/database/data/view"
import { TableWorkbench, type TableWorkbenchHandle } from "@/components/database/data/workbench"

const NO_STAGED: Record<string, number> = {}

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max)

type Select = ReturnType<typeof useDatabase>["select"]

/**
 * The page's writer of the address: the context's `select`, with two faults
 * of the context made good for this page's own keys until they are mended
 * there (both are in this area's contract as requests).
 *
 * It never forgets a write. It keeps the last one — "from this address, to
 * that one" — and applies it again whenever the reader comes back to the
 * address it started from: a table opened from the rail, filtered, and
 * pressed in the rail again bounced back to the filtered view. So each time
 * the router arrives somewhere, a write of nothing is made from there, which
 * takes the place of the kept one and changes no key.
 *
 * And it loses a write that returns to the address the router still shows
 * while an earlier one is on its way: Structure then Data ended on Structure,
 * a filter applied and removed at once came back. `address.ts` tells such a
 * late landing from the reader going somewhere, and the last write is made
 * again — before the page is painted on the address that was overtaken.
 */
function useAddress(select: Select, param: (name: string) => string): Select {
  const search = useSearchParams()
  const router = addressKey(addressOf((name) => search.get(name) ?? ""))
  const shown = addressOf(param)
  const flight = useRef<Flight>(SETTLED)
  const latest = useRef({ select, shown, router })
  useLayoutEffect(() => {
    latest.current = { select, shown, router }
  })
  useLayoutEffect(() => {
    const read = landed(flight.current, router, performance.now())
    flight.current = read.flight
    latest.current.select(read.rewrite ? addressParams(read.rewrite) : {})
  }, [router])
  return useCallback<Select>((params) => {
    const now = latest.current
    flight.current = wrote(flight.current, now.shown, now.router, params, performance.now())
    now.select(params)
  }, [])
}

type Shown = { schema: string; table: string }
type Pending = { heading: string; run: () => void }

/**
 * The SQL table editor: a rail of what holds rows, the table open beside it,
 * and the active row read down in an inspector — one frame, three columns.
 *
 * Which table is open is the address. That is what makes a table a link, and
 * Back a way out of a foreign key; it also means the address can move to
 * another table without asking — Back, a pasted link, the command palette.
 * So the editor does not follow the address blindly: while edits are staged
 * it keeps showing the table they were made on and asks first, and puts the
 * address back if the reader stays.
 *
 * Staged edits of a table with a key are kept for the tab under that table's
 * name, in memory and never on disk (they are rows of somebody's data). The
 * few ways out that cannot be asked about — Back to another page of the
 * dashboard — therefore lose nothing: the set is there when the reader
 * returns, and the rail marks the table that holds one.
 */
export function SqlData() {
  const { id, engine, selection, param, select: selectUnkept, href, goto, readOnly } = useDatabase()
  const { can } = useAuth()
  const router = useRouter()
  const select = useAddress(selectUnkept, param)

  // The columns are laid out by the width of the frame they share, not the
  // window's (`panes.ts`). Before it is measured — one render, never painted —
  // the window answers.
  const [frame, frameWidth] = useColumnWidth<HTMLDivElement>()
  const windowWide = useMediaQuery("(min-width: 1024px)")
  const [railPinned, setRailPinned] = useViewState("databases.data.rail", true)
  const [railWidth, setRailWidth, resetRailWidth] = usePanelSize(
    "databases.data.rail",
    RAIL.fallback,
  )
  const [inspecting, setInspecting] = useViewState("databases.data.inspector", false)
  const [inspectorWidth, setInspectorWidth, resetInspectorWidth] = usePanelSize(
    "databases.data.inspector",
    INSPECTOR.fallback,
  )
  const panes = arrange(
    frameWidth > 0 ? frameWidth : windowWide ? 1440 : 0,
    railWidth,
    inspectorWidth,
    railPinned,
  )
  const railBeside = panes.rail === "beside"

  const catalog = useCatalog(id, selection.schema)
  const exporter = useExport(id)

  /* ------------------------------------------------- which table is shown */

  const wanted = useMemo<Shown | null>(
    () => (selection.table ? { schema: selection.schema, table: selection.table } : null),
    [selection.schema, selection.table],
  )
  const [shown, setShown] = useState<Shown | null>(wanted)
  const wantedKey = wanted ? tableKey(wanted.schema, wanted.table) : ""
  const shownKey = shown ? tableKey(shown.schema, shown.table) : ""
  const sameTable = wantedKey === shownKey

  // Where the rail lies over the table it is what opens when no table is, and
  // it is put away when the address names one: by the rail's own link, by a
  // followed key, by Back.
  const [railOver, setRailOver] = useState(wantedKey === "")
  const [railFor, setRailFor] = useState(wantedKey)
  if (railFor !== wantedKey) {
    setRailFor(wantedKey)
    setRailOver(wantedKey === "")
  }

  const detail = useTableDetail(id, shown?.schema ?? "", shown?.table ?? "", shown !== null)
  // Only a key that identifies a row makes a set that can be found again
  // after the page was left: positions mean nothing on another visit.
  const keyed = Boolean(
    detail.data && detail.data.primaryKey.length > 0 && engine.capabilities.rowIdentity !== "none",
  )
  const scope = `${id}.${shownKey}`
  const [kept, setKept] = useMemoryState<ChangeSetState>(
    `databases.${id}.data.changes.${shownKey}`,
    EMPTY_CHANGE_STATE,
  )
  const store = useMemo(() => ({ state: kept, setState: setKept }), [kept, setKept])
  const changeSet = useChangeSet({ scope, store: keyed ? store : undefined })
  const dirty = changeSet.dirty

  // The address moved to another table. With nothing staged the editor
  // follows; with edits staged it stays, and the guard below asks.
  if (!sameTable && !dirty) setShown(wanted)

  // The view of the shown table: the address while the editor follows it, and
  // what it last said about this table once it has moved on — which is what
  // the address is put back to if the reader stays.
  //
  // Moving on is not only another table. The address can ask for other rows
  // of the same one without passing the guard — the table's own link in the
  // rail, a key that points back into its table, Back and Forward — and rows
  // read again under a staged set are the wrong rows under it: a table with
  // no key ties an edit to where its row stood. So with edits staged the
  // editor holds the rows as well as the table, and asks.
  const stated = useMemo(() => readView(param), [param])
  const [held, setHeld] = useState({ table: shownKey, view: stated })
  // What is held was said of another table: this one starts from the address.
  const fresh = held.table !== shownKey
  const followed = sameTable && (!dirty || fresh || sameRows(held.view, stated))
  if (followed && (fresh || JSON.stringify(held.view) !== JSON.stringify(stated))) {
    setHeld({ table: shownKey, view: stated })
  }
  const view = followed ? stated : held.view

  const setView = useCallback((patch: Partial<ViewState>) => select(viewParams(patch)), [select])

  /* ------------------------------------------------------------ the guard */

  const [pending, setPending] = useState<Pending | null>(null)
  const workbench = useRef<TableWorkbenchHandle>(null)
  const guard = useCallback(
    (heading: string, run: () => void) => {
      if (dirty) setPending({ heading, run })
      else run()
    },
    [dirty],
  )
  // The question was asked over the grid, by nothing the keyboard can be
  // handed back to: it goes to the rows the reader was working on.
  const toRows = () => focusAfterDialog(() => workbench.current?.focus())
  const stay = () => {
    setPending(null)
    if (!followed && shown) {
      select({ schema: shown.schema || null, table: shown.table, ...viewParams(held.view) })
    }
    toRows()
  }
  const discard = () => {
    changeSet.reset()
    const next = pending
    setPending(null)
    next?.run()
    toRows()
  }
  useUnloadGuard(dirty)
  useLinkGuard(
    dirty,
    useCallback(
      (target: string) =>
        setPending({ heading: "Leaving the table editor", run: () => router.push(target) }),
      [router],
    ),
  )

  /* ------------------------------------------- what the rail knows of sets */

  const [staged, setStaged] = useMemoryState<Record<string, number>>(
    `databases.${id}.data.staged`,
    NO_STAGED,
  )
  const total = keyed ? changeSet.counts.total : 0
  useEffect(() => {
    if (!shownKey || !detail.data) return
    setStaged((before) => {
      if ((before[shownKey] ?? 0) === total) return before
      const next = { ...before }
      if (total > 0) next[shownKey] = total
      else delete next[shownKey]
      return next
    })
  }, [shownKey, detail.data, total, setStaged])

  /* ---------------------------------------------------------- rail verbs */

  const refreshCatalog = catalog.refresh
  const onChanged = useCallback(
    (object: DbCatalogObject, change: "truncate" | "drop") => {
      refreshCatalog()
      const open = shown !== null && shown.table === object.name && shown.schema === object.schema
      if (!open) return
      // The table that was open is empty now, or gone.
      changeSet.reset()
      if (change === "drop")
        select({
          table: null,
          ...viewParams({ ...held.view, view: "data", filters: [], sort: [], page: 1 }),
        })
      else {
        detail.refresh()
        workbench.current?.reload()
      }
    },
    [refreshCatalog, shown, changeSet, select, held, detail],
  )
  const verbs = useTableVerbs({ onExport: exporter.run, onChanged })

  /* ---------------------------------------------- a file as a new table */

  const [importingNew, setImportingNew] = useState(false)
  const madeTable = useRef<string | null>(null)

  /* --------------------------------------------------------------- render */

  // A row is open beside the rows: with room for only one side column, the
  // rail steps aside for it and comes back when it closes.
  const rowOpen = inspecting && shown !== null && view.view === "data" && detail.data !== undefined
  const steppedAside = panes.inspector === "alone" && rowOpen
  const railShown = railBeside ? railPinned && !steppedAside : railOver
  const toggleRail = () => {
    if (!railBeside) setRailOver(!railOver)
    else if (steppedAside) {
      // One side column fits: asking for the tables puts the row away.
      setInspecting(false)
      setRailPinned(true)
    } else setRailPinned(!railPinned)
  }
  const railColumn = clamp(railWidth, RAIL.min, panes.railMax)

  const listed = catalog.data?.schema ?? selection.schema
  // The address names a schema the connection does not have.
  const unknown =
    catalog.data !== undefined &&
    catalog.data.schemas.length > 0 &&
    !catalog.data.schemas.some((schema) => schema.name === listed)
  const canCreate =
    can("service.control") && !readOnly && engine.capabilities.ddlOperations.includes("createTable")
  // A file can be brought in as a table of its own where a table can be made.
  const canImportNew =
    canCreate && !unknown && engine.can("import") && engine.can("importCreateTable")
  const taken = useMemo(
    () =>
      ROW_GROUPS.flatMap(({ group }) =>
        (catalog.data?.objects[group] ?? []).map((object) => object.name),
      ),
    [catalog.data],
  )
  const newTarget = useMemo(
    () => ({ kind: "new" as const, schema: listed, taken }),
    [listed, taken],
  )
  // The schema is read and holds nothing that has rows.
  const empty =
    catalog.data !== undefined &&
    !unknown &&
    ROW_GROUPS.every(({ group }) => (catalog.data?.objects[group]?.length ?? 0) === 0)

  return (
    <SectionFrame section="data">
      {/* One frame around the whole workbench (§7.2): the rail, the table and
          the row are columns of one working surface, with a hairline between
          them rather than a gutter and three borders. */}
      <div
        ref={frame}
        data-slot="table-editor"
        style={{ "--jd-data-rail": `${railColumn}px` } as React.CSSProperties}
        className="relative flex min-h-0 min-w-0 flex-1 overflow-hidden rounded-xl border bg-card"
      >
        {railShown && (
          <div
            className={cn(
              "flex min-h-0 flex-col border-hairline bg-card",
              railBeside
                ? "relative w-(--jd-data-rail) shrink-0 border-r"
                : "absolute inset-0 z-30",
            )}
            // Lying over the table, the rail is put away by choosing one. After
            // the link has acted, not before: taken off the page first, the
            // link was followed by the browser instead — the whole document
            // read again, and the staged edits with no question asked.
            onClick={(event) => {
              if (railBeside || !event.defaultPrevented) return
              const link = event.target instanceof Element ? event.target.closest("a[href]") : null
              if (link instanceof HTMLAnchorElement && new URL(link.href).searchParams.get("table"))
                setRailOver(false)
            }}
          >
            <TableRail
              catalog={catalog}
              current={shown}
              staged={staged}
              verbsFor={verbs.verbsFor}
              onImportNew={canImportNew ? () => setImportingNew(true) : undefined}
              saidBeside={railBeside && shown === null}
            />
            {railBeside ? (
              <ResizeHandle
                side="left"
                label="Tables width"
                value={railColumn}
                min={RAIL.min}
                max={panes.railMax}
                onChange={(px, commit) => setRailWidth(clamp(px, RAIL.min, panes.railMax), commit)}
                onReset={resetRailWidth}
                className="absolute inset-y-0 -right-1 z-20"
              />
            ) : (
              shown && (
                <div className="shrink-0 border-t border-hairline bg-surface-header p-2">
                  <Button
                    size="sm"
                    variant="outline"
                    className="w-full"
                    onClick={() => setRailOver(false)}
                  >
                    <span className="min-w-0 truncate">Back to {shown.table}</span>
                  </Button>
                </div>
              )
            )}
          </div>
        )}

        {shown ? (
          <TableWorkbench
            ref={workbench}
            key={shownKey}
            schema={shown.schema}
            table={shown.table}
            detail={detail}
            view={view}
            setView={setView}
            changeSet={changeSet}
            guard={guard}
            railOpen={railShown}
            onToggleRail={toggleRail}
            panel={{
              open: inspecting,
              setOpen: setInspecting,
              over: panes.inspector === "over",
              width: panes.inspectorWidth,
              max: panes.inspectorMax,
              setWidth: setInspectorWidth,
              resetWidth: resetInspectorWidth,
            }}
            onExport={exporter.run}
            exporting={exporter.running !== null}
            onWritten={refreshCatalog}
          />
        ) : (
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            {!railShown && (
              <div className="flex h-10 shrink-0 items-center border-b border-hairline bg-surface-header px-1.5">
                <IconAction label="Show the tables" className="size-7" onClick={toggleRail}>
                  <SidebarLeftOpen />
                </IconAction>
              </div>
            )}
            {catalog.data && !unknown && !empty ? (
              <SchemaLanding catalog={catalog.data} railShown={railShown} onShowRail={toggleRail} />
            ) : (
              <div className="flex min-h-0 flex-1 items-center justify-center p-6">
                {catalog.error && !catalog.data ? (
                  // Why the list could not be read is said once, here, with the
                  // way to ask again; the rail beside it only says that it is empty.
                  <div className="w-full max-w-md space-y-3">
                    <ErrorState error={catalog.error} />
                    <Button size="sm" variant="outline" onClick={catalog.refresh}>
                      Try again
                    </Button>
                  </div>
                ) : !catalog.data ? (
                  // The catalogue is on its way: the shape of what it will say.
                  <div
                    role="status"
                    aria-label={`Reading the ${engine.nouns.objects}`}
                    className="w-full max-w-xl space-y-6"
                  >
                    <div className="flex items-center gap-3">
                      <Skeleton className="size-10 rounded-lg" />
                      <div className="space-y-2">
                        <Skeleton className="h-3.5 w-28" />
                        <Skeleton className="h-3 w-64 max-w-full" />
                      </div>
                    </div>
                    <div className="flex gap-6">
                      {["w-12", "w-10", "w-14", "w-16"].map((width) => (
                        <div key={width} className="space-y-2">
                          <Skeleton className="h-2.5 w-10" />
                          <Skeleton className={cn("h-3.5", width)} />
                        </div>
                      ))}
                    </div>
                    <div className="space-y-4">
                      {["w-40", "w-28", "w-32", "w-24"].map((width) => (
                        <div key={width} className="space-y-1.5">
                          <Skeleton className={cn("h-3", width)} />
                          <Skeleton className="h-1 w-full" />
                        </div>
                      ))}
                    </div>
                  </div>
                ) : unknown ? (
                  // A schema that is not there is not an empty one: nothing can
                  // be made in it, and the way on is the one that exists.
                  <EmptyState
                    className="border-0"
                    icon={Table}
                    title={`No ${engine.nouns.container} called ${listed}`}
                    description={`This connection has no such ${engine.nouns.container}. It may have been dropped since the link was made.`}
                    action={
                      <Button size="sm" variant="outline" asChild>
                        <Link
                          href={href("data", {
                            schema: catalog.data.defaultSchema || null,
                            table: null,
                          })}
                        >
                          Open{" "}
                          {catalog.data.defaultSchema || `the default ${engine.nouns.container}`}
                        </Link>
                      </Button>
                    }
                  />
                ) : (
                  <EmptyState
                    className="border-0"
                    mark={<EngineMark engine={engine} />}
                    title={`No ${engine.nouns.objects} yet`}
                    description={`${listed || `This ${engine.nouns.container}`} holds nothing with rows in it.`}
                    action={
                      !railShown ? (
                        <Button size="sm" variant="outline" onClick={toggleRail}>
                          Show the {engine.nouns.objects}
                        </Button>
                      ) : (
                        canCreate && (
                          <Button size="sm" variant="outline" asChild>
                            <Link
                              href={href("schema", {
                                schema: listed || null,
                                table: null,
                                new: "table",
                              })}
                            >
                              <Plus />
                              New {engine.nouns.object}
                            </Link>
                          </Button>
                        )
                      )
                    }
                  />
                )}
              </div>
            )}
          </div>
        )}
      </div>

      <LeaveGuard
        open={pending !== null || (!followed && dirty)}
        table={shown?.table ?? ""}
        counts={changeSet.counts}
        heading={
          pending?.heading ??
          (sameTable
            ? "Showing other rows"
            : wanted
              ? `Opening ${wanted.table}`
              : "Closing this table")
        }
        onStay={stay}
        onReview={() => {
          stay()
          workbench.current?.review()
        }}
        onDiscard={discard}
      />
      {verbs.dialog}
      {canImportNew && (
        <ImportDialog
          open={importingNew}
          onOpenChange={(next) => {
            setImportingNew(next)
            // Closed over a table it made: that table is where the reader is taken.
            const table = madeTable.current
            madeTable.current = null
            if (!next && table) goto("data", { schema: listed || null, table })
          }}
          target={newTarget}
          onImported={(table) => {
            refreshCatalog()
            madeTable.current = table
          }}
        />
      )}
    </SectionFrame>
  )
}

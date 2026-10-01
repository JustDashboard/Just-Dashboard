"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Plus, SidebarLeftOpen } from "@/components/icons"
import { usePanelSize } from "@/lib/panel-size"
import { cn } from "@/lib/utils"
import { useMemoryState, useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { IconAction } from "@/components/icon-action"
import { ResizeHandle } from "@/components/resize-handle"
import { EmptyState, ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { EMPTY_CHANGE_STATE, useChangeSet, type ChangeSetState } from "@/components/database/grid"
import { EngineMark, SectionFrame } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { LeaveGuard, useLinkGuard, useUnloadGuard } from "@/components/database/data/guard"
import { ROW_GROUPS } from "@/components/database/data/kinds"
import { TableRail, tableKey } from "@/components/database/data/rail"
import { useTableVerbs } from "@/components/database/data/table-verbs"
import type { DbCatalogObject } from "@/components/database/data/types"
import { useExport } from "@/components/database/data/use-export"
import { useCatalog, useTableDetail } from "@/components/database/data/use-table"
import { readView, sameRows, viewParams, type ViewState } from "@/components/database/data/view"
import { TableWorkbench, type TableWorkbenchHandle } from "@/components/database/data/workbench"

const RAIL = { min: 200, max: 480, fallback: 256 }
const NO_STAGED: Record<string, number> = {}

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max)

/**
 * Lets go of the last write to the address once the address has moved.
 *
 * The section's context keeps the last `select` — "from this address, to that
 * one" — and applies it for as long as the router shows the address it
 * started from. It never forgets it, so it applies it again whenever the
 * reader comes back to that address by a link: a table opened from the rail,
 * filtered, and pressed in the rail again bounced back to the filtered view;
 * and a reader who chose "Keep editing" over another table could press that
 * table a second time and have nothing happen at all, not even the question.
 *
 * A write of nothing, made from the address the router has arrived at, takes
 * the place of the kept one and changes no key. The context is not this
 * area's to change; the request to drop the write there is in the contract.
 */
function useSettledAddress(select: (params: Record<string, never>) => void) {
  const address = useSearchParams().toString()
  const write = useRef(select)
  useEffect(() => {
    write.current = select
  })
  useEffect(() => {
    write.current({})
  }, [address])
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
  const { id, engine, selection, param, select, href, readOnly } = useDatabase()
  const { can } = useAuth()
  const router = useRouter()
  useSettledAddress(select)
  const wide = useMediaQuery("(min-width: 1024px)")

  const [railPinned, setRailPinned] = useViewState("databases.data.rail", true)
  // On a phone the rail lies over the table, and is what opens when no table is.
  const [railOver, setRailOver] = useState(!selection.table)
  const [railWidth, setRailWidth, resetRailWidth] = usePanelSize(
    "databases.data.rail",
    RAIL.fallback,
  )

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
  const stay = () => {
    setPending(null)
    if (!followed && shown) {
      select({ schema: shown.schema || null, table: shown.table, ...viewParams(held.view) })
    }
  }
  const discard = () => {
    changeSet.reset()
    const next = pending
    setPending(null)
    next?.run()
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

  /* --------------------------------------------------------------- render */

  const railShown = wide ? railPinned : railOver
  const toggleRail = () => (wide ? setRailPinned(!railPinned) : setRailOver(!railOver))
  const listed = catalog.data?.schema ?? selection.schema
  const canCreate =
    can("service.control") && !readOnly && engine.capabilities.ddlOperations.includes("createTable")
  // The schema is read and holds nothing that has rows.
  const empty =
    catalog.data !== undefined &&
    ROW_GROUPS.every(({ group }) => (catalog.data?.objects[group]?.length ?? 0) === 0)

  return (
    <SectionFrame section="data">
      {/* One frame around the whole workbench (§7.2): the rail, the table and
          the row are columns of one working surface, with a hairline between
          them rather than a gutter and three borders. */}
      <div
        data-slot="table-editor"
        style={{ "--jd-data-rail": `${railWidth}px` } as React.CSSProperties}
        className="relative flex min-h-0 min-w-0 flex-1 overflow-hidden rounded-xl border bg-card"
      >
        {railShown && (
          <div
            className={cn(
              "flex min-h-0 flex-col border-hairline bg-card",
              wide ? "relative w-(--jd-data-rail) shrink-0 border-r" : "absolute inset-0 z-30",
            )}
            // On a phone the rail lies over the table; choosing one puts it away.
            onClickCapture={(event) => {
              if (wide) return
              if (event.target instanceof Element && event.target.closest("a[href]"))
                setRailOver(false)
            }}
          >
            <TableRail
              catalog={catalog}
              current={shown}
              staged={staged}
              verbsFor={verbs.verbsFor}
              errorBeside={wide && shown === null}
            />
            {wide ? (
              <ResizeHandle
                side="left"
                label="Tables width"
                value={railWidth}
                min={RAIL.min}
                max={RAIL.max}
                onChange={(px, commit) => setRailWidth(clamp(px, RAIL.min, RAIL.max), commit)}
                onReset={resetRailWidth}
                className="absolute inset-y-0 -right-1 z-20"
              />
            ) : (
              <div className="shrink-0 border-t border-hairline bg-surface-header p-2">
                <Button
                  size="sm"
                  variant="outline"
                  className="w-full"
                  onClick={() => setRailOver(false)}
                >
                  Back to {shown ? shown.table : "the table"}
                </Button>
              </div>
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
            wide={wide}
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
              ) : (
                <EmptyState
                  className="border-0"
                  mark={<EngineMark engine={engine} />}
                  title={empty ? `No ${engine.nouns.objects} yet` : `Pick a ${engine.nouns.object}`}
                  description={
                    empty
                      ? `${listed || `This ${engine.nouns.container}`} holds nothing with rows in it.`
                      : `Its rows open here, to read, filter and edit.${railShown ? " They are listed on the left." : ""}`
                  }
                  action={
                    !railShown ? (
                      <Button size="sm" variant="outline" onClick={toggleRail}>
                        Show the {engine.nouns.objects}
                      </Button>
                    ) : (
                      canCreate &&
                      empty && (
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
    </SectionFrame>
  )
}

"use client"

import { useCallback, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { Eye, FolderPlus, Puzzle, SidebarLeftOpen, Table, Trash } from "@/components/icons"
import { usePanelSize } from "@/lib/panel-size"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { FormFact } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { ResizeHandle } from "@/components/resize-handle"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import type { Verb } from "@/components/verbs"
import { useCatalog, useTableDetail } from "@/components/database/data/use-table"
import { ReadFailed } from "@/components/database/fleet/read-failed"
import { SectionFrame } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  objectParams,
  readAddress,
  schemaParams,
  tableParams,
  type TableViewId,
} from "@/components/database/schema/address"
import { dropSchemaRequest } from "@/components/database/schema/changes"
import { SchemaLanding } from "@/components/database/schema/landing"
import { NewTablePanel } from "@/components/database/schema/new-table"
import { EnumDialog, SchemaDialog, ViewDialog } from "@/components/database/schema/object-forms"
import { ObjectView } from "@/components/database/schema/object-view"
import { SchemaRail } from "@/components/database/schema/rail"
import { TableView } from "@/components/database/schema/table-view"
import { useDestroy } from "@/components/database/schema/use-destroy"
import { useSettledAddress } from "@/components/database/schema/use-settled-address"

const RAIL = { min: 200, max: 480, fallback: 272 }
const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max)

/**
 * The schema browser of a SQL engine: everything a schema holds, by kind, in
 * a rail — and beside it the object chosen, read and changed.
 *
 * What is open is the address: the schema, the table (or the object, by its
 * kind and name), which reading of a table, and whether a creation form was
 * asked for (`?new=table` is how the table editor's "New table" arrives). So
 * an object is a link, Back is a way out of one, and a pasted address opens
 * exactly what it names.
 *
 * Every change here is made through a form that shows the statement the
 * server will run before its one command runs it, and a table is always its
 * schema and its name together — never the name alone.
 */
export function SqlSchema() {
  const { id, engine, selection, param, select, href, goto, readOnly } = useDatabase()
  const { can } = useAuth()
  useSettledAddress()
  const address = useMemo(() => readAddress(selection, param), [selection, param])
  const { selected, creating } = address

  const catalog = useCatalog(id, selection.schema)
  const table = selected?.type === "table" ? selected : null
  const detail = useTableDetail(id, table?.schema ?? "", table?.name ?? "", table !== null)
  const { destroy, dialog } = useDestroy()

  /* --------------------------------------------------- how the columns lie */

  const beside = useMediaQuery("(min-width: 1024px)")
  const [railPinned, setRailPinned] = useViewState("databases.schema.rail", true)
  const [railWidth, setRailWidth, resetRailWidth] = usePanelSize(
    "databases.schema.rail",
    RAIL.fallback,
  )
  // Where the rail lies over the object it is what opens when nothing is
  // chosen, and it is put away when the address names something.
  const selectedKey = selected ? `${selected.type}:${selected.schema}:${selected.name}` : ""
  const [railOver, setRailOver] = useState(selectedKey === "")
  const [railFor, setRailFor] = useState(selectedKey)
  if (railFor !== selectedKey) {
    setRailFor(selectedKey)
    setRailOver(selectedKey === "")
  }
  const railShown = beside ? railPinned : railOver
  const toggleRail = () => (beside ? setRailPinned(!railPinned) : setRailOver(!railOver))
  const railColumn = clamp(railWidth, RAIL.min, RAIL.max)

  /* ------------------------------------------------- what may be made here */

  const data = catalog.data
  const listed = data?.schema ?? selection.schema
  const info = data?.schemas.find((schema) => schema.name === listed)
  const unknown = data !== undefined && data.schemas.length > 0 && !info
  const operations = engine.capabilities.ddlOperations
  // Changes are made in a schema of the reader's own: not in the engine's
  // namespaces, and — on an engine with one schema to choose from — only in
  // the one its names resolve to.
  const changeable =
    data !== undefined &&
    !unknown &&
    !info?.system &&
    (engine.can("schemas") || listed === data.defaultSchema)
  const mayWrite = can("service.control") && !readOnly
  const mayDestroy = can("destructive") && !readOnly

  const creations = useMemo<Verb[]>(() => {
    if (!mayWrite) return []
    const open = (what: string) => () => select({ new: what })
    return [
      ...(changeable && operations.includes("createTable")
        ? [{ key: "table", label: `New ${engine.nouns.object}`, icon: Table, run: open("table") }]
        : []),
      ...(changeable && operations.includes("views")
        ? [{ key: "view", label: "New view", icon: Eye, run: open("view") }]
        : []),
      ...(changeable && operations.includes("enumTypes")
        ? [{ key: "type", label: "New enum type", icon: Puzzle, run: open("type") }]
        : []),
      ...(operations.includes("schemas")
        ? [
            {
              key: "schema",
              label: `New ${engine.nouns.container}`,
              icon: FolderPlus,
              run: open("schema"),
            },
          ]
        : []),
    ]
  }, [mayWrite, changeable, operations, engine, select])
  const may = (what: string) => creations.some((verb) => verb.key === what)

  const refreshCatalog = catalog.refresh
  const refreshDetail = detail.refresh
  // "Read the schema again" is asked of the tree and means the page: the
  // object open beside it was read at the same moment and is as stale.
  const [asked, setAsked] = useState(0)
  const refreshAll = useCallback(() => {
    refreshCatalog()
    refreshDetail()
    setAsked((n) => n + 1)
  }, [refreshCatalog, refreshDetail])

  // A form that made something goes to what it made, and that address no
  // longer asks for the form. Closing it must not then write the old address
  // back over the new one.
  const leaving = useRef(false)
  const closeForm = useCallback(() => {
    if (leaving.current) leaving.current = false
    else select({ new: null })
  }, [select])
  const openMade = useCallback(
    (params: Record<string, string | null>) => {
      leaving.current = true
      refreshCatalog()
      goto("schema", params)
    },
    [refreshCatalog, goto],
  )

  const schemaVerbs: Verb[] =
    mayDestroy && changeable && operations.includes("schemas") && listed !== data?.defaultSchema
      ? [
          {
            key: "drop",
            label: `Drop ${engine.nouns.container}…`,
            icon: Trash,
            danger: true,
            run: () =>
              void destroy({
                request: dropSchemaRequest(listed),
                title: `Drop ${engine.nouns.container}`,
                subject: {
                  name: listed,
                  facts: info && info.tables >= 0 && (
                    <FormFact label="Tables and views">{info.tables}</FormFact>
                  ),
                },
                sentence: `The ${engine.nouns.container} is removed. The engine refuses while anything is still in it: empty it first.`,
                done: `Dropped ${listed}`,
                onDone: () => select(schemaParams(data?.defaultSchema ?? "")),
              }),
          },
        ]
      : []

  // What the engine's forms cannot make, said where the command would be.
  const limit =
    mayWrite && changeable && !operations.includes("createTable")
      ? `A ${engine.label} ${engine.nouns.object} is made in Query: it needs choices this form cannot make for you.`
      : undefined

  const toggle = (
    <div className="flex h-10 shrink-0 items-center border-b border-hairline bg-surface-header px-1.5">
      <IconAction label="Show the objects" className="size-7 max-sm:size-8" onClick={toggleRail}>
        <SidebarLeftOpen />
      </IconAction>
    </div>
  )

  return (
    <SectionFrame section="schema">
      {/* One frame around the whole workbench (§7.2): the tree and the object
          are two columns of one surface, with a hairline between them. */}
      <div
        data-slot="schema-browser"
        style={{ "--jd-schema-rail": `${railColumn}px` } as React.CSSProperties}
        className="relative flex min-h-0 min-w-0 flex-1 overflow-hidden rounded-xl border bg-card"
      >
        {railShown && (
          <div
            className={cn(
              "flex min-h-0 flex-col border-hairline bg-card",
              beside ? "relative w-(--jd-schema-rail) shrink-0 border-r" : "absolute inset-0 z-30",
            )}
          >
            <SchemaRail
              catalog={catalog}
              selected={selected}
              creations={creations}
              onRefresh={refreshAll}
              saidBeside={beside && selected === null}
            />
            {beside ? (
              <ResizeHandle
                side="left"
                label="Objects width"
                value={railColumn}
                min={RAIL.min}
                max={RAIL.max}
                onChange={(px, commit) => setRailWidth(clamp(px, RAIL.min, RAIL.max), commit)}
                onReset={resetRailWidth}
                className="absolute inset-y-0 -right-1 z-20"
              />
            ) : (
              selected && (
                <div className="shrink-0 border-t border-hairline bg-surface-header p-2">
                  <Button
                    size="sm"
                    variant="outline"
                    className="w-full"
                    onClick={() => setRailOver(false)}
                  >
                    <span className="min-w-0 truncate">Back to {selected.name}</span>
                  </Button>
                </div>
              )
            )}
          </div>
        )}

        {table ? (
          <TableView
            key={`${table.schema}\u0000${table.name}`}
            schema={table.schema}
            name={table.name}
            view={address.view}
            onView={(view: TableViewId) => select({ view: view === "columns" ? null : view })}
            detail={detail}
            catalog={data}
            asked={asked}
            railOpen={railShown}
            onToggleRail={toggleRail}
            onChanged={() => {
              refreshDetail()
              refreshCatalog()
            }}
            onRenamed={(to) => {
              refreshCatalog()
              select({ table: to })
            }}
            onDropped={() => {
              refreshCatalog()
              select({ table: null, view: null })
            }}
          />
        ) : selected?.type === "object" ? (
          <ObjectView
            key={`${selected.kind}:${selected.schema}:${selected.name}:${selected.signature}:${selected.table}`}
            selected={selected}
            asked={asked}
            railOpen={railShown}
            onToggleRail={toggleRail}
            onChanged={refreshCatalog}
          />
        ) : data && !unknown ? (
          <SchemaLanding
            catalog={data}
            railShown={railShown}
            onShowRail={toggleRail}
            creations={creations}
            verbs={schemaVerbs}
            note={limit}
          />
        ) : (
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            {!railShown && toggle}
            <div className="flex min-h-0 flex-1 items-center justify-center p-6">
              {catalog.error && !data ? (
                <ReadFailed
                  error={catalog.error}
                  onRetry={refreshCatalog}
                  className="w-full max-w-md"
                />
              ) : !data ? (
                // The catalogue is on its way: the shape of what it will say.
                <div
                  role="status"
                  aria-label="Reading the schema"
                  className="w-full max-w-2xl space-y-6"
                >
                  <div className="flex items-center gap-3">
                    <Skeleton className="size-10 rounded-lg" />
                    <div className="space-y-2">
                      <Skeleton className="h-3.5 w-28" />
                      <Skeleton className="h-3 w-56 max-w-full" />
                    </div>
                  </div>
                  <div className="flex gap-6">
                    {["w-8", "w-6", "w-8", "w-6", "w-8"].map((width, index) => (
                      <div key={index} className="space-y-2">
                        <Skeleton className="h-2.5 w-12" />
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
              ) : (
                // A schema that is not there is not an empty one: nothing can
                // be made in it, and the way on is the one that exists.
                <EmptyState
                  className="border-0"
                  title={`No ${engine.nouns.container} called ${listed}`}
                  description={`This connection has no such ${engine.nouns.container}. It may have been dropped since the link was made.`}
                  action={
                    <Button size="sm" variant="outline" asChild>
                      <Link href={href("schema", schemaParams(data.defaultSchema))}>
                        Open {data.defaultSchema || `the default ${engine.nouns.container}`}
                      </Link>
                    </Button>
                  }
                />
              )}
            </div>
          </div>
        )}
      </div>

      {may("table") && (
        <NewTablePanel
          open={creating === "table"}
          schema={listed}
          schemas={data?.schemas ?? []}
          onClose={closeForm}
          onCreated={(schema, name) => {
            refreshCatalog()
            goto("schema", tableParams(schema, name))
          }}
        />
      )}
      {creating === "view" && may("view") && (
        <ViewDialog
          schemas={data?.schemas ?? []}
          schema={listed}
          onClose={closeForm}
          onDone={() => undefined}
          onMade={(made) => openMade(tableParams(made.schema, made.name))}
        />
      )}
      {creating === "type" && may("type") && (
        <EnumDialog
          schemas={data?.schemas ?? []}
          schema={listed}
          onClose={closeForm}
          onDone={() => undefined}
          onMade={(made) => openMade(objectParams({ kind: "enum", ...made }))}
        />
      )}
      {creating === "schema" && may("schema") && (
        <SchemaDialog
          onClose={closeForm}
          onDone={() => undefined}
          onMade={(name) => openMade(schemaParams(name))}
        />
      )}
      {dialog}
    </SectionFrame>
  )
}

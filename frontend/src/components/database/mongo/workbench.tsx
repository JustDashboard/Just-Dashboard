"use client"

import { useState } from "react"
import { Copy, Download, Pencil, SidebarLeftOpen, Trash } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePanelSize } from "@/lib/panel-size"
import { useViewState } from "@/lib/view-state"
import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { IconAction } from "@/components/icon-action"
import { ResizeHandle } from "@/components/resize-handle"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import type { Verb } from "@/components/verbs"
import type { SectionId } from "@/components/database/engine"
import { EngineMark, SectionFrame } from "@/components/database/kit"
import {
  NewCollectionDialog,
  RenameCollectionDialog,
  dropRequest,
} from "@/components/database/mongo/collection-dialogs"
import { DatabasePane } from "@/components/database/mongo/database-pane"
import type { QueryDraft } from "@/components/database/mongo/query"
import { CollectionRail } from "@/components/database/mongo/rail"
import { ExportDialog, useMongoExport } from "@/components/database/mongo/transfer"
import type { MongoCollection } from "@/components/database/mongo/types"
import {
  useCatalog,
  useMongo,
  type Catalog,
  type Mongo,
} from "@/components/database/mongo/use-mongo"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"

const RAIL = { base: 300, min: 220, max: 520 }

/** The three workbenches a collection opens in, in the rail's order. */
const WORK: SectionId[] = ["data", "query", "schema"]

/** What a workbench's page is handed once a collection is open. */
export type Workbench = {
  mongo: Mongo
  catalog: Catalog
  /** The open collection as the list knows it; `undefined` while the list is unread. */
  collection: MongoCollection | undefined
  /** The control that brings the rail back, for the start of the page's own strip. */
  leading: React.ReactNode
  confirm: (request: ConfirmRequest) => void
  /** Opens Export for the open collection, with the query on screen when there is one. */
  exportCollection: (draft?: QueryDraft) => void
  exporting: boolean
  /** Opens New collection; absent where the role, the engine or the connection cannot. */
  newCollection: (() => void) | undefined
}

/**
 * The frame the three MongoDB workbenches share: the collections of one
 * database beside the page's own work on the one that is open.
 *
 * One frame, two columns with a hairline between them. Which database and
 * which collection are in the address, so a pasted link opens on the same
 * collection, and the rail's rows open theirs in whichever of Documents,
 * Aggregations and Schema the reader is on. Creating, renaming, dropping and
 * exporting a collection live here, because the rail that offers them is
 * here.
 *
 * The readings a report page would carry as tiles are where the reader is
 * already looking: a count and a size on every row of the rail, their sums
 * at its foot, and — while no collection is open — what the database is made
 * of in the pane where a collection would be.
 */
export function MongoWorkbench({
  section,
  standalone,
  collectionFree,
  children,
}: {
  section: SectionId
  /**
   * The page has a view that needs no collection (the command console): it
   * is handed the frame even while none is open, and draws `DatabasePane`
   * itself where its view does need one.
   */
  standalone?: boolean
  /**
   * The view on screen needs no collection. On a phone the rail stands over
   * the page until a collection is chosen; over such a view it must not.
   */
  collectionFree?: boolean
  children: (workbench: Workbench) => React.ReactNode
}) {
  const mongo = useMongo()
  const { id, engine, database, collection, goto, select, param, canWrite, canDestroy } = mongo
  const catalog = useCatalog(mongo)
  const { confirm, dialog } = useConfirm()
  useFocusReturn()
  const exporter = useMongoExport()

  // New collection is a state of the address (`?new=collection`), the way the
  // SQL pages' links ask for New table: another page can link straight to it.
  const creating = param("new") === "collection"
  const setCreating = (open: boolean) => select({ new: open ? "collection" : null })
  const [renaming, setRenaming] = useState<MongoCollection | null>(null)
  const [exporting, setExporting] = useState<{ collection: string; draft?: QueryDraft } | null>(
    null,
  )

  // The rail beside the work where there is room for both, and over it where
  // there is not: a phone shows the collections until one is opened.
  const wide = useMediaQuery("(min-width: 1024px)")
  const [railShown, setRailShown] = useViewState(`databases.${id}.mongo.rail`, true)
  const [railOver, setRailOver] = useState(false)
  const [heldCollection, setHeldCollection] = useState(collection)
  if (heldCollection !== collection) {
    // Choosing a collection on a phone puts the rail away.
    setHeldCollection(collection)
    setRailOver(false)
  }
  const railVisible = wide ? railShown : railOver || (!collection && !collectionFree)
  const [railWidth, setRailWidth, resetRailWidth] = usePanelSize("databases.mongo.rail", RAIL.base)
  const railPx = Math.min(Math.max(railWidth, RAIL.min), RAIL.max)

  const listed = catalog.collections.data?.collections ?? []
  const canCreate = canWrite && engine.can("collections") && Boolean(database)

  const verbsFor = (entry: MongoCollection): Verb[] => {
    const elsewhere: Verb[] = WORK.filter((other) => other !== section && engine.has(other)).map(
      (other) => {
        const page = engine.section(other)!
        return {
          key: `open-${other}`,
          label: `Open in ${page.title}`,
          icon: page.icon,
          run: () => goto(other, { db: database, collection: entry.name }),
        }
      },
    )
    return [
      ...elsewhere,
      {
        key: "copy",
        label: "Copy name",
        icon: Copy,
        run: () => void copyText(entry.name, "Name copied"),
      },
      ...(engine.can("export")
        ? [
            {
              key: "export",
              label: "Export…",
              icon: Download,
              run: () => setExporting({ collection: entry.name }),
            },
          ]
        : []),
      ...(canWrite && !entry.system && entry.type !== "view"
        ? [{ key: "rename", label: "Rename…", icon: Pencil, run: () => setRenaming(entry) }]
        : []),
      ...(canDestroy && !entry.system
        ? [
            {
              key: "drop",
              label: "Drop…",
              icon: Trash,
              danger: true,
              run: () =>
                confirm(
                  dropRequest(mongo, entry, () => {
                    catalog.refresh()
                    // The one on screen is gone: the pane says what the database holds.
                    if (entry.name === collection) select({ collection: null })
                  }),
                ),
            },
          ]
        : []),
    ]
  }

  const leading = !railVisible && (
    <IconAction
      label={`Show the ${engine.nouns.objects}`}
      aria-pressed={false}
      className="size-7 shrink-0"
      onClick={() => (wide ? setRailShown(true) : setRailOver(true))}
    >
      <SidebarLeftOpen />
    </IconAction>
  )

  const current = catalog.current
  // A name the list does not have, once the list is read: said, not drawn as empty.
  const missing = Boolean(collection) && catalog.collections.data !== undefined && !current

  const workbench: Workbench = {
    mongo,
    catalog,
    collection: current,
    leading,
    confirm,
    exportCollection: (draft) => setExporting({ collection, draft }),
    exporting: exporter.running !== null,
    newCollection: canCreate ? () => setCreating(true) : undefined,
  }

  return (
    <SectionFrame section={section}>
      {dialog}
      {/* One frame around the workbench: the rail and the work are two
          columns of one surface, with a hairline between them. */}
      <div
        style={{ "--jd-mongo-rail": `${railPx}px` } as React.CSSProperties}
        className="relative flex min-h-0 min-w-0 flex-1 overflow-hidden rounded-xl border bg-card"
      >
        {railVisible && (
          <div className="relative flex shrink-0 border-hairline bg-card max-lg:absolute max-lg:inset-0 max-lg:z-20 lg:w-(--jd-mongo-rail) lg:border-r">
            <CollectionRail
              mongo={mongo}
              catalog={catalog}
              section={section}
              verbsFor={verbsFor}
              onNew={canCreate ? () => setCreating(true) : undefined}
              onHide={() => (wide ? setRailShown(false) : setRailOver(false))}
            />
            <ResizeHandle
              side="left"
              label="Collections panel width"
              value={railPx}
              min={RAIL.min}
              max={RAIL.max}
              onChange={(px, commit) => setRailWidth(px, commit)}
              onReset={resetRailWidth}
              className="absolute inset-y-0 -right-1 z-20 max-lg:hidden"
            />
          </div>
        )}
        {standalone && !collection ? (
          children(workbench)
        ) : !collection ? (
          <DatabasePane
            mongo={mongo}
            catalog={catalog}
            section={section}
            leading={leading}
            onNew={canCreate ? () => setCreating(true) : undefined}
          />
        ) : missing ? (
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            {leading && (
              <div className="flex h-10 shrink-0 items-center border-b border-hairline bg-surface-header px-3">
                {leading}
              </div>
            )}
            <EmptyState
              mark={<EngineMark engine={engine} />}
              className="min-h-0 flex-1 border-0"
              title={`No ${engine.nouns.object} called ${collection}`}
              description={`${database} has no such ${engine.nouns.object}. It may have been dropped or renamed since the link was made.`}
              action={
                <Button size="sm" variant="outline" onClick={() => select({ collection: null })}>
                  See what {database} holds
                </Button>
              }
            />
          </div>
        ) : (
          children(workbench)
        )}
      </div>

      {canCreate && (
        <NewCollectionDialog
          mongo={mongo}
          collections={listed}
          open={creating}
          onOpenChange={setCreating}
          onCreated={(name) => {
            catalog.refresh()
            goto(section, { db: database, collection: name })
          }}
        />
      )}
      {canWrite && (
        <RenameCollectionDialog
          mongo={mongo}
          collection={renaming}
          collections={listed}
          confirm={confirm}
          onOpenChange={(open) => !open && setRenaming(null)}
          onRenamed={(from, to) => {
            catalog.refresh()
            if (from === collection) select({ collection: to })
          }}
        />
      )}
      <ExportDialog
        mongo={mongo}
        request={
          exporting && {
            target: { id, database, collection: exporting.collection },
            draft: exporting.draft,
          }
        }
        onOpenChange={(open) => !open && setExporting(null)}
        onExport={(request) => void exporter.run(request)}
      />
    </SectionFrame>
  )
}

/** The strip at the top of a workbench's own column: the collection's name, then the page's controls. */
export function WorkbenchHead({
  leading,
  children,
}: {
  leading?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="flex min-h-10 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline bg-surface-header px-3 py-1">
      {leading}
      {children}
    </div>
  )
}

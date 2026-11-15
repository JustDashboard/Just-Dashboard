"use client"

import { useState } from "react"
import {
  Copy,
  Download,
  FileText,
  Layers,
  Pencil,
  SettingsSliders,
  SidebarLeftClose,
  SidebarLeftOpen,
  Trash,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePanelSize } from "@/lib/panel-size"
import { useMemoryState, useViewState } from "@/lib/view-state"
import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { PaneBar } from "@/components/phone-panes"
import { ResizeHandle } from "@/components/resize-handle"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import type { Verb } from "@/components/verbs"
import { useUnloadGuard } from "@/components/database/data/guard"
import type { SectionId } from "@/components/database/engine"
import { EngineMark, SectionFrame } from "@/components/database/kit"
import { stagedKey, withoutStaged, type StagedEdits } from "@/components/database/mongo/changes"
import {
  CollectionOptionsDialog,
  NewCollectionDialog,
  RenameCollectionDialog,
  dropRequest,
  mayModify,
  type ViewPreset,
} from "@/components/database/mongo/collection-dialogs"
import { DatabasePane } from "@/components/database/mongo/database-pane"
import { collectionKey, type QueryDraft } from "@/components/database/mongo/query"
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
import { WORKBENCH_PAGE, WorkbenchFrame } from "@/components/database/workbench/chrome"

const RAIL = { base: 300, min: 220, max: 520 }
const NO_VERBS: Verb[] = []

/** The three workbenches a collection opens in, in the rail's order. */
const WORK: SectionId[] = ["data", "query", "schema"]

/** What a workbench's page is handed once a collection is open. */
export type Workbench = {
  mongo: Mongo
  catalog: Catalog
  /** The open collection as the list knows it; `undefined` while the list is unread. */
  collection: MongoCollection | undefined
  /**
   * What stands at the start of the page's own strip. It was the control
   * that brought the rail back; the frame's head and a phone's bar do that
   * now, so it is nothing, and kept for the pages that still take it.
   */
  leading: React.ReactNode
  confirm: (request: ConfirmRequest) => void
  /** Opens Export for the open collection, with the query on screen when there is one. */
  exportCollection: (draft?: QueryDraft) => void
  exporting: boolean
  /**
   * Opens New collection — on a view through a pipeline, when one is given.
   * Absent where the role, the engine or the connection cannot.
   */
  newCollection: ((view?: ViewPreset) => void) | undefined
  /**
   * Opens the options of the open collection — a cap, an expiry, a view's
   * definition. Absent where it has none that can be changed, or the reader
   * may not change them.
   */
  collectionOptions: (() => void) | undefined
}

/**
 * The frame the three MongoDB workbenches share: the collections of one
 * database beside the page's own work on the one that is open.
 *
 * One frame, two columns with a hairline between them. Which database and
 * which collection are in the address, so a pasted link opens on the same
 * collection, and the rail's rows open theirs in whichever of Documents,
 * Aggregations and Schema the reader is on. Creating, renaming, changing the
 * options of, dropping and exporting a collection live here, because the rail
 * that offers them is here.
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
  const [preset, setPreset] = useState<ViewPreset>()
  // A view the dialog was opened on belongs to that opening alone.
  if (!creating && preset) setPreset(undefined)
  const [renaming, setRenaming] = useState<MongoCollection | null>(null)
  const [modifying, setModifying] = useState<MongoCollection | null>(null)
  // Field edits staged on documents and not yet sent, in any collection here.
  // They are kept while the reader moves about the database, and a reload or
  // a closed tab would lose them: the browser asks first.
  const [staged, setStaged] = useMemoryState<StagedEdits>(stagedKey(id), {})
  useUnloadGuard(Object.keys(staged).length > 0)
  const unsent = (name: string) => Object.keys(staged[collectionKey(database, name)] ?? {}).length
  /** A collection that is gone, or goes by another name, takes the edits staged on its documents with it. */
  const forget = (name: string) =>
    setStaged((held) => withoutStaged(held, collectionKey(database, name)))
  const [exporting, setExporting] = useState<{ collection: string; draft?: QueryDraft } | null>(
    null,
  )

  // The rail beside the work where there is room for both, and over it where
  // there is not: a phone shows the collections until one is opened.
  const wide = useMediaQuery("(min-width: 1024px)")
  const [railShown, setRailShown] = useViewState(`databases.${id}.mongo.rail`, true)
  // On a phone the frame shows one pane at a time and a bar at its foot
  // moves between them. It opens on the collections while none is chosen —
  // unless the view on screen needs none — and choosing one puts the rail
  // away; with none chosen the other pane is what the database holds.
  const [railOver, setRailOver] = useState(!collection && !collectionFree)
  const [heldCollection, setHeldCollection] = useState(collection)
  if (heldCollection !== collection) {
    setHeldCollection(collection)
    setRailOver(!collection && !collectionFree)
  }
  const railVisible = wide ? railShown : railOver
  const [railWidth, setRailWidth, resetRailWidth] = usePanelSize("databases.mongo.rail", RAIL.base)
  const railPx = Math.min(Math.max(railWidth, RAIL.min), RAIL.max)

  const listed = catalog.collections.data?.collections ?? []
  const canCreate = canWrite && engine.can("collections") && Boolean(database)

  /** Opens New collection, on a view through a pipeline when one is given. */
  const openNew = canCreate
    ? (view?: ViewPreset) => {
        setPreset(view)
        setCreating(true)
      }
    : undefined

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
      ...(mayModify(mongo, entry)
        ? [
            {
              key: "options",
              label: "Options…",
              icon: SettingsSliders,
              run: () => setModifying(entry),
            },
          ]
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
                    forget(entry.name)
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

  // The rail is brought back from the head's menu beside the work, and by
  // the bar on a phone: the pages' own strips start with what is open.
  const leading = null
  const page = engine.section(section)

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
    newCollection: openNew,
    collectionOptions:
      current && mayModify(mongo, current) ? () => setModifying(current) : undefined,
  }

  return (
    <SectionFrame section={section} className={WORKBENCH_PAGE}>
      {dialog}
      {/* One frame around the workbench: the head that says which database,
          then the rail and the work as two columns of one surface, with a
          hairline between them. */}
      <WorkbenchFrame
        section={section}
        slot="mongo-workbench"
        style={{ "--jd-mongo-rail": `${railPx}px` } as React.CSSProperties}
        menu={
          wide
            ? [
                {
                  key: "rail",
                  label: `${railShown ? "Hide" : "Show"} the ${engine.nouns.objects}`,
                  icon: railShown ? SidebarLeftClose : SidebarLeftOpen,
                  run: () => setRailShown(!railShown),
                },
              ]
            : NO_VERBS
        }
        bar={
          !wide && (
            <PaneBar<"rail" | "work">
              label={page?.title ?? "Collections"}
              active={railVisible ? "rail" : "work"}
              onPick={(pane) => setRailOver(pane === "rail")}
              stops={[
                { key: "rail", label: "Collections", icon: <Layers /> },
                {
                  key: "work",
                  label: page?.title ?? "Documents",
                  icon: page ? <page.icon /> : <FileText />,
                },
              ]}
            />
          )
        }
      >
        {railVisible && (
          <div className="relative flex shrink-0 border-hairline bg-card max-lg:absolute max-lg:inset-0 max-lg:z-20 lg:w-(--jd-mongo-rail) lg:border-r">
            <CollectionRail
              mongo={mongo}
              catalog={catalog}
              section={section}
              verbsFor={verbsFor}
              unsent={unsent}
              onNew={openNew && (() => openNew())}
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
            onNew={openNew && (() => openNew())}
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
      </WorkbenchFrame>

      {canCreate && (
        <NewCollectionDialog
          mongo={mongo}
          collections={listed}
          // Opened on a view, the dialog starts from that view; else from nothing.
          key={preset ? `view:${preset.viewOn}:${preset.pipeline}` : "new"}
          view={preset}
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
            forget(from)
            catalog.refresh()
            if (from === collection) select({ collection: to })
          }}
        />
      )}
      {canWrite && (
        <CollectionOptionsDialog
          mongo={mongo}
          collection={modifying}
          collections={listed}
          confirm={confirm}
          onOpenChange={(open) => !open && setModifying(null)}
          onChanged={catalog.refresh}
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

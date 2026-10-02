"use client"

import { tabClasses } from "@/components/tabs"
import { useDatabase } from "@/components/database/shell/database-context"
import { ConsoleView } from "@/components/database/mongo/aggregations/console"
import { PipelineView } from "@/components/database/mongo/aggregations/pipeline-view"
import { DatabasePane } from "@/components/database/mongo/database-pane"
import {
  MongoWorkbench,
  WorkbenchHead,
  type Workbench,
} from "@/components/database/mongo/workbench"

type View = "pipeline" | "command"
const VIEWS: { id: View; label: string }[] = [
  { id: "pipeline", label: "Pipeline" },
  { id: "command", label: "Command" },
]

/**
 * Aggregations: the pipeline builder for the open collection, and beside it
 * the command console.
 *
 * Two views of one page. The builder is about a collection — which one is in
 * the address, and its stages are kept per collection, so the pipeline on
 * screen is always that collection's and never the one before's. The console
 * is about a database and needs no collection at all. Which view is in the
 * address too (`?view=command`).
 */
export function MongoAggregations() {
  const { param, engine } = useDatabase()
  return (
    <MongoWorkbench
      section="query"
      standalone
      collectionFree={param("view") === "command" && engine.can("console")}
    >
      {(workbench) => <Aggregations {...workbench} />}
    </MongoWorkbench>
  )
}

function Aggregations(workbench: Workbench) {
  const { mongo, catalog, collection, leading, confirm, newCollection } = workbench
  const { param, select, engine } = mongo
  const console_ = engine.can("console")
  const view: View = param("view") === "command" && console_ ? "command" : "pipeline"
  const views = console_ ? VIEWS : VIEWS.slice(0, 1)
  const open = Boolean(mongo.collection) && collection !== undefined

  return (
    <div data-slot="mongo-aggregations" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <WorkbenchHead leading={leading}>
        {/* Pressed buttons, not a landmark: two views of one page. */}
        <div role="group" aria-label="Aggregation views" className="-my-1 flex gap-1 self-stretch">
          {views.map((entry) => (
            <button
              key={entry.id}
              type="button"
              aria-pressed={view === entry.id}
              onClick={() => select({ view: entry.id === "pipeline" ? null : entry.id })}
              className={tabClasses(view === entry.id, "h-10")}
            >
              {entry.label}
            </button>
          ))}
        </div>
      </WorkbenchHead>
      {view === "command" ? (
        <ConsoleView mongo={mongo} catalog={catalog} confirm={confirm} />
      ) : open ? (
        <PipelineView
          // A collection's pipeline is its own: another collection starts from its own stages.
          key={`${mongo.database}\u0000${mongo.collection}`}
          mongo={mongo}
          collection={collection}
          confirm={confirm}
        />
      ) : (
        <DatabasePane mongo={mongo} catalog={catalog} section="query" onNew={newCollection} />
      )}
    </div>
  )
}

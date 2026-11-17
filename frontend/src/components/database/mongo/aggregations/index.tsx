"use client"

import { ViewTabs } from "@/components/tabs"
import { TabsContent } from "@/components/ui/tabs"
import { useDatabase } from "@/components/database/shell/database-context"
import { ConsoleView } from "@/components/database/mongo/aggregations/console"
import { PipelineView } from "@/components/database/mongo/aggregations/pipeline-view"
import { DatabasePane } from "@/components/database/mongo/database-pane"
import { MongoWorkbench, type Workbench } from "@/components/database/mongo/workbench"

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
  const { mongo, catalog, collection, confirm, newCollection } = workbench
  const { param, select, engine } = mongo
  const console_ = engine.can("console")
  const view: View = param("view") === "command" && console_ ? "command" : "pipeline"
  const views = console_ ? VIEWS : VIEWS.slice(0, 1)
  const open = Boolean(mongo.collection) && collection !== undefined

  return (
    // Two views of one page, as every workbench offers its views: a strip on
    // the object band's ground.
    <ViewTabs
      label="Aggregation views"
      views={views.map((entry) => ({ key: entry.id, label: entry.label }))}
      value={view}
      onValueChange={(next) => select({ view: next === "pipeline" ? null : next })}
      className="bg-surface-header px-1.5 [&_[role=tab]]:h-[39px]"
      rootClassName="min-h-0 flex-1"
    >
      <TabsContent
        data-slot="mongo-aggregations"
        value={view}
        className="flex min-h-0 min-w-0 flex-col"
      >
        {view === "command" ? (
          <ConsoleView mongo={mongo} catalog={catalog} confirm={confirm} />
        ) : open ? (
          <PipelineView
            // A collection's pipeline is its own: another collection starts from its own stages.
            key={`${mongo.database}\u0000${mongo.collection}`}
            mongo={mongo}
            collection={collection}
            confirm={confirm}
            newCollection={newCollection}
          />
        ) : (
          <DatabasePane mongo={mongo} catalog={catalog} section="query" onNew={newCollection} />
        )}
      </TabsContent>
    </ViewTabs>
  )
}

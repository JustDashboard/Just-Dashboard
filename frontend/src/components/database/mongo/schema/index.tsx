"use client"

import { bytes, plural } from "@/lib/format"
import { ViewTabList } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import {
  CollectionKindTag,
  CollectionMark,
  collectionKind,
} from "@/components/database/mongo/kinds"
import { AnalysisView } from "@/components/database/mongo/schema/analysis"
import { IndexesView } from "@/components/database/mongo/schema/indexes"
import { ValidationView } from "@/components/database/mongo/schema/validation"
import { MongoWorkbench, type Workbench } from "@/components/database/mongo/workbench"
import { ObjectHead } from "@/components/database/workbench/chrome"

type View = "analysis" | "indexes" | "validation"

/**
 * Schema: what a collection holds, what it is indexed by, and what it
 * accepts.
 *
 * A collection has no declared schema, so the three views are the three
 * things that stand in for one: the fields its documents actually have, read
 * off a sample; its indexes, with how much each is used; and its validation
 * rule, with how many stored documents break it. Which view is in the
 * address (`?view=`).
 */
export function MongoSchema() {
  return (
    <MongoWorkbench section="schema">
      {(workbench) => (
        <Schema
          // A collection's analysis and its rule are its own.
          key={`${workbench.mongo.database}\u0000${workbench.mongo.collection}`}
          {...workbench}
        />
      )}
    </MongoWorkbench>
  )
}

function Schema(workbench: Workbench) {
  const { mongo, collection: info } = workbench
  const { collection, engine, param, select } = mongo
  const views: { id: View; label: string }[] = [
    ...(engine.can("schemaAnalysis") ? [{ id: "analysis" as const, label: "Analysis" }] : []),
    ...(engine.can("indexes") ? [{ id: "indexes" as const, label: "Indexes" }] : []),
    ...(engine.can("validation") ? [{ id: "validation" as const, label: "Validation" }] : []),
  ]
  const asked = param("view")
  const view = views.some((entry) => entry.id === asked) ? (asked as View) : views[0]?.id
  // A server with no view of the schema to offer still reads its analysis.
  const shown: View = view ?? "analysis"
  const kind = info ? collectionKind(info) : "collection"

  return (
    <Tabs
      data-slot="mongo-schema"
      value={shown}
      onValueChange={(next) => select({ view: next === views[0]?.id ? null : next })}
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-0"
    >
      <ObjectHead
        mark={<CollectionMark kind={kind} />}
        name={collection}
        // What it holds stands beside its name from `md`, on the head's one
        // line, as on Documents: a second line under the name made the head
        // taller than the rail's head beside it, and a row taller on a phone.
        kind={
          <span className="flex items-center gap-2">
            <CollectionKindTag kind={kind} />
            {info?.statsKnown && (
              <span className="numeric text-hint text-muted-foreground max-md:hidden">
                {plural(info.count, "document")} · {bytes(info.size)}
              </span>
            )}
          </span>
        }
        views={
          views.length > 0 && (
            // Three readings of one collection, as every workbench offers its views.
            <ViewTabList
              label="Schema views"
              value={shown}
              views={views.map((entry) => ({
                key: entry.id,
                label: entry.label,
                count: entry.id === "indexes" && info?.statsKnown ? info.indexCount : undefined,
              }))}
              className="border-b-0"
            />
          )
        }
      />
      <TabsContent value={shown} className="flex min-h-0 min-w-0 flex-col">
        {view === "indexes" ? (
          <IndexesView
            mongo={mongo}
            catalog={workbench.catalog}
            collection={info}
            confirm={workbench.confirm}
            onChanged={workbench.catalog.refresh}
          />
        ) : view === "validation" ? (
          <ValidationView {...workbench} />
        ) : (
          <AnalysisView mongo={mongo} collection={info} />
        )}
      </TabsContent>
    </Tabs>
  )
}

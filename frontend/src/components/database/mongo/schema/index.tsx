"use client"

import { bytes } from "@/lib/format"
import { ChipCount, tabClasses } from "@/components/tabs"
import { grouped } from "@/components/database/data/view"
import {
  CollectionKindTag,
  CollectionMark,
  collectionKind,
} from "@/components/database/mongo/kinds"
import { AnalysisView } from "@/components/database/mongo/schema/analysis"
import { IndexesView } from "@/components/database/mongo/schema/indexes"
import { ValidationView } from "@/components/database/mongo/schema/validation"
import {
  MongoWorkbench,
  WorkbenchHead,
  type Workbench,
} from "@/components/database/mongo/workbench"

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
  const { mongo, collection: info, leading } = workbench
  const { collection, engine, param, select } = mongo
  const views: { id: View; label: string }[] = [
    ...(engine.can("schemaAnalysis") ? [{ id: "analysis" as const, label: "Analysis" }] : []),
    ...(engine.can("indexes") ? [{ id: "indexes" as const, label: "Indexes" }] : []),
    ...(engine.can("validation") ? [{ id: "validation" as const, label: "Validation" }] : []),
  ]
  const asked = param("view")
  const view = views.some((entry) => entry.id === asked) ? (asked as View) : views[0]?.id
  const kind = info ? collectionKind(info) : "collection"

  return (
    <div data-slot="mongo-schema" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <WorkbenchHead leading={leading}>
        <h2 className="flex min-w-0 items-center gap-1.5">
          <CollectionMark kind={kind} />
          <span className="min-w-0 truncate font-mono text-sm leading-6 font-medium">
            {collection}
          </span>
        </h2>
        <CollectionKindTag kind={kind} />
        {info?.statsKnown && (
          <span className="numeric min-w-0 truncate text-hint text-muted-foreground max-md:hidden">
            {grouped(info.count)} {info.count === 1 ? "document" : "documents"} · {bytes(info.size)}
          </span>
        )}
        <span className="min-w-0 flex-1" />
        {/* Pressed buttons, not a landmark: three readings of one collection. */}
        <div role="group" aria-label="Schema views" className="-my-1 flex gap-1 self-stretch">
          {views.map((entry) => (
            <button
              key={entry.id}
              type="button"
              aria-pressed={view === entry.id}
              onClick={() => select({ view: entry.id === views[0].id ? null : entry.id })}
              className={tabClasses(view === entry.id, "h-10")}
            >
              {entry.label}
              {entry.id === "indexes" && info?.statsKnown && (
                <ChipCount>{info.indexCount}</ChipCount>
              )}
            </button>
          ))}
        </div>
      </WorkbenchHead>
      {view === "indexes" ? (
        <IndexesView mongo={mongo} confirm={workbench.confirm} />
      ) : view === "validation" ? (
        <ValidationView {...workbench} />
      ) : (
        <AnalysisView mongo={mongo} collection={info} />
      )}
    </div>
  )
}

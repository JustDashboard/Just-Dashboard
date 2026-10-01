"use client"

import { AreaStub } from "@/components/database/shell/area-stub"

/** Documents in three views behind a query bar. */
export function MongoDocuments() {
  return (
    <AreaStub section="data" area="mongo">
      Documents as a list, a table or JSON, behind a query bar with filter, projection and sort.
    </AreaStub>
  )
}

/** The aggregation builder, with a preview after each stage. */
export function MongoAggregations() {
  return (
    <AreaStub section="query" area="mongo">
      The aggregation builder: a pipeline stage by stage, with what each stage returns.
    </AreaStub>
  )
}

/** Schema analysis by sample, indexes and validation rules. */
export function MongoSchema() {
  return (
    <AreaStub section="schema" area="mongo">
      What a collection holds: its fields by sample, its indexes and its validation rules.
    </AreaStub>
  )
}

/** Current operations, server status and the profiler. */
export function MongoPerformance() {
  return (
    <AreaStub section="performance" area="mongo">
      What the server is doing: current operations, server status and the slow operations the
      profiler kept.
    </AreaStub>
  )
}

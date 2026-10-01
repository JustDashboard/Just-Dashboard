"use client"

import { MongoAggregations } from "@/components/database/mongo"
import { SqlQuery } from "@/components/database/query"
import { RedisConsole } from "@/components/database/redis"
import { SectionPage } from "@/components/database/shell/section-page"

export default function QueryPage() {
  return (
    <SectionPage
      section="query"
      sql={SqlQuery}
      keyvalue={RedisConsole}
      document={MongoAggregations}
    />
  )
}

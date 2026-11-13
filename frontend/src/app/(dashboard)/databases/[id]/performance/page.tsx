"use client"

import { MongoPerformance } from "@/components/database/mongo/performance"
import { SqlPerformance } from "@/components/database/ops/performance"
import { RedisPerformance } from "@/components/database/redis/performance"
import { SectionPage } from "@/components/database/shell/section-page"

export default function PerformancePage() {
  return (
    <SectionPage
      section="performance"
      sql={SqlPerformance}
      keyvalue={RedisPerformance}
      document={MongoPerformance}
    />
  )
}

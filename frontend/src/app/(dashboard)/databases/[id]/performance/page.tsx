"use client"

import { MongoPerformance } from "@/components/database/mongo"
import { SqlPerformance } from "@/components/database/ops"
import { RedisPerformance } from "@/components/database/redis"
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

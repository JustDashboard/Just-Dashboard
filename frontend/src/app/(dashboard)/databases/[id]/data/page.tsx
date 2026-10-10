"use client"

import { SqlData } from "@/components/database/data"
import { MongoDocuments } from "@/components/database/mongo"
import { RedisKeys } from "@/components/database/redis"
import { SectionPage } from "@/components/database/shell/section-page"

export default function DataPage() {
  return <SectionPage section="data" sql={SqlData} keyvalue={RedisKeys} document={MongoDocuments} />
}

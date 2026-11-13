"use client"

import { SqlData } from "@/components/database/data"
import { MongoDocuments } from "@/components/database/mongo/documents"
import { RedisKeys } from "@/components/database/redis/keys"
import { SectionPage } from "@/components/database/shell/section-page"

export default function DataPage() {
  return <SectionPage section="data" sql={SqlData} keyvalue={RedisKeys} document={MongoDocuments} />
}

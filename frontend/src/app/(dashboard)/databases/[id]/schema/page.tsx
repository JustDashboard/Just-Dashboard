"use client"

import { MongoSchema } from "@/components/database/mongo"
import { SqlSchema } from "@/components/database/schema"
import { SectionPage } from "@/components/database/shell/section-page"

export default function SchemaPage() {
  return <SectionPage section="schema" sql={SqlSchema} document={MongoSchema} />
}

"use client"

import { MongoAccess } from "@/components/database/ops/access-mongo"
import { RedisAccess } from "@/components/database/ops/access-redis"
import { SqlAccess } from "@/components/database/ops/access-sql"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Who can sign in to the server behind this connection, and what each may
 * do — in the engine's own vocabulary. A SQL server has roles with
 * attributes and grants on databases, schemas and tables; a key–value server
 * has ACL users, each a rule of commands and key patterns; a document
 * database has users holding roles on databases. They are three pages behind
 * one address, chosen by what the registry says the engine is.
 */
export function Access() {
  const { engine } = useDatabase()
  if (engine.can("aclRules")) return <RedisAccess />
  if (engine.kind === "document") return <MongoAccess />
  return <SqlAccess />
}

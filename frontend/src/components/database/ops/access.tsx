"use client"

import { useEffect } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { SectionFrame } from "@/components/database/kit"
import { MongoAccess } from "@/components/database/ops/access-mongo"
import { RedisAccess } from "@/components/database/ops/access-redis"
import { SqlAccess } from "@/components/database/ops/access-sql"
import { formerAccountHref } from "@/components/database/ops/account-address"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Who can sign in to the server behind this connection, and what each may
 * do — in the engine's own vocabulary. A SQL server has roles with
 * attributes and grants on databases, schemas and tables; a key–value server
 * has ACL users, each a rule of commands and key patterns; a document
 * database has users holding roles on databases. They are three pages behind
 * one address, chosen by what the registry says the engine is.
 *
 * An account opened in a sheet over this list until 0.8.7, named in the
 * address (`?account=`, `?host=`). It is a page of its own now
 * (`/access/<account>`), and an address from then is sent on to it.
 */
export function Access() {
  const { id, engine } = useDatabase()
  const router = useRouter()
  const former = formerAccountHref(id, useSearchParams())
  useEffect(() => {
    // Replaced, not pushed: the old address should not be what Back returns to.
    if (former) router.replace(former)
  }, [router, former])
  if (former) return <SectionFrame section="access">{null}</SectionFrame>
  if (engine.can("aclRules")) return <RedisAccess />
  if (engine.kind === "document") return <MongoAccess />
  return <SqlAccess />
}

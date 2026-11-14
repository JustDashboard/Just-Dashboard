"use client"

import { usePathname } from "next/navigation"
import { SqlAccount } from "@/components/database/ops/access-account"
import { MongoAccount } from "@/components/database/ops/access-mongo-account"
import { RedisAccount } from "@/components/database/ops/access-redis-account"
import { accountFromPath } from "@/components/database/ops/account-address"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * One account of the server behind this connection, at
 * `/databases/<id>/access/<account>`: a SQL role, a key–value server's ACL
 * user or a document database's user, chosen by what the registry says the
 * engine is, as the list is (`access.tsx`).
 *
 * The page is keyed on the account, so what one holds unsaved — staged
 * grants, a rule being rewritten — is never drawn under another's name when
 * the rail walks to the next.
 */
export function AccountPage() {
  const { engine, param } = useDatabase()
  const name = accountFromPath(usePathname()) ?? ""
  // `host` is absent for an engine whose accounts have none, and may be the
  // empty string for one whose accounts do.
  const host = param("host") || undefined
  if (engine.can("aclRules")) return <RedisAccount key={name} name={name} />
  if (engine.kind === "document") {
    return <MongoAccount key={`${host}.${name}`} name={name} db={host} />
  }
  return <SqlAccount key={`${name}@${host}`} name={name} host={host} />
}

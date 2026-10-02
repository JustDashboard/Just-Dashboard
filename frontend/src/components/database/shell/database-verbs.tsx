"use client"

import { RefreshClockwise } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useLifecycleVerbs } from "@/components/database/home/verbs"
import { useOperateVerbs } from "@/components/database/ops/verbs"
import { useDatabase } from "@/components/database/shell/database-context"
import type { DatabaseVerbTools } from "@/components/database/shell/types"

/**
 * The verbs of one database, behind one menu (§13): on the strip of every
 * page, and at the end of the home's identity line.
 *
 * The menu is assembled here and written elsewhere. What the server is asked
 * to do — start, stop, restart, protect — belongs to the home
 * (`home/verbs.ts`), and what is done with the database as a thing kept —
 * back it up, forget it — to the operations pages (`ops/verbs.ts`). Each
 * declares its verbs as data, already filtered by what the role may do and
 * what this server can, and this draws them in that order under the one verb
 * the shell has of its own: asking again whether the server answers.
 */
export function DatabaseVerbs() {
  const { conn, status } = useDatabase()
  const { confirm, dialog } = useConfirm()
  const tools: DatabaseVerbTools = { confirm }
  const verbs: Verb[] = [
    { key: "check", label: "Check connection", icon: RefreshClockwise, run: status.refresh },
    ...useLifecycleVerbs(tools),
    ...useOperateVerbs(tools),
  ]
  return (
    <>
      <VerbMenu verbs={verbs} label={`Actions for ${conn.name}`} />
      {dialog}
    </>
  )
}

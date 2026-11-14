"use client"

import { usePathname } from "next/navigation"
import { RefreshClockwise } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useLifecycleVerbs, usePowerChange } from "@/components/database/home/verbs"
import { useOperateVerbs } from "@/components/database/ops/verbs"
import { useDatabase } from "@/components/database/shell/database-context"
import { databasePlace } from "@/components/database/shell/routes"
import type { DatabaseVerbTools } from "@/components/database/shell/types"

/**
 * The ways into the database that are not the header's one command, as words
 * in its menu: the page statements are written on, in the engine's own word
 * for it. It was a button beside the command on the home; three commands of
 * equal weight had no first one, and the page is in the rail under Work
 * besides.
 *
 * It is in the menu only while the server answers and from a page that is not
 * that one, and waits while the server is being started, stopped or
 * restarted: there is nothing behind it to open. A workbench's own head draws
 * the same menu (`workbench/chrome.tsx`), so it reads this too.
 */
export function useOpenVerbs(): Verb[] {
  const { conn, engine, status, goto } = useDatabase()
  const pathname = usePathname()
  const change = usePowerChange(conn.id)
  const query = engine.section("query")
  if (!query || status.state !== "running") return []
  if (databasePlace(pathname)?.section === "query") return []
  return [
    {
      key: "query",
      label: query.title,
      icon: query.icon,
      disabled: Boolean(change),
      run: () => goto("query"),
    },
  ]
}

/**
 * The verbs of one database, behind one menu (§13): at the end of the header
 * on every page of it, and of the strip where a page keeps the strip.
 *
 * The menu is assembled here and written elsewhere. What the server is asked
 * to do — start, stop, restart, protect — belongs to the home
 * (`home/verbs.ts`), and what is done with the database as a thing kept —
 * back it up, forget it — to the operations pages (`ops/verbs.ts`). Each
 * declares its verbs as data, already filtered by what the role may do and
 * what this server can, and this draws them in that order under the two the
 * shell has of its own: the way to the page statements are written on, and
 * asking again whether the server answers.
 */
export function DatabaseVerbs() {
  const { conn, status } = useDatabase()
  const { confirm, dialog } = useConfirm()
  const tools: DatabaseVerbTools = { confirm }
  const verbs: Verb[] = [
    ...useOpenVerbs(),
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

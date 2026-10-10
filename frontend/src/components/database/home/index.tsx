"use client"

import type { ComponentType } from "react"
import type { EngineKind } from "@/components/database/engine"
import { SectionFrame } from "@/components/database/kit"
import { DownHome } from "@/components/database/home/down"
import { DocumentHome } from "@/components/database/home/mongo-home"
import { KeyValueHome } from "@/components/database/home/redis-home"
import { SqlHome } from "@/components/database/home/sql-home"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The home each family of engine has. The registry says which family a
 * connection is; nothing here asks what the driver is called.
 */
const HOMES: Partial<Record<EngineKind, ComponentType>> = {
  sql: SqlHome,
  keyvalue: KeyValueHome,
  document: DocumentHome,
}

/**
 * A database's front page: this engine's control panel.
 *
 * It starts with the identity and verbs, recorded activity, attention and
 * rankings, then reference facts. Each engine family supplies its own blocks.
 *
 * A server that is not answering — stopped, paused, refusing, or a saved
 * connection that can no longer be opened — keeps its home: the same line,
 * what state it is in, the one thing to do about it, and the facts that need
 * no dial, including retained history. Its engine is asked nothing until it is back.
 *
 * The page's name is the `h1` `SectionFrame` writes for assistive technology
 * (§14); the name drawn on the line is the switcher, which is a control.
 */
export function DatabaseHome() {
  const { engine, status } = useDatabase()
  const Home = HOMES[engine.kind]
  const answering = status.state === "running"
  return (
    <SectionFrame section="home">
      {answering && Home ? <Home key="answering" /> : <DownHome key="down" />}
    </SectionFrame>
  )
}

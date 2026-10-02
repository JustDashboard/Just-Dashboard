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
 * It reads top to bottom as the host Overview does (§15) — what this is
 * (`HomeIdentity`), its headline figures as tiles with a ceiling or a trend
 * where one exists, one chart of what it has been doing while the page was
 * open, what needs somebody beside what it spends its time on, what it holds
 * beside what uses it, and the reference facts last. Which figures, and which
 * blocks, are the engine family's own: a SQL server, a key–value store and a
 * document database each have a home built from the same blocks.
 *
 * A server that is not answering — stopped, paused, refusing, or a saved
 * connection that can no longer be opened — keeps its home: the same line,
 * what state it is in, the one thing to do about it, and the facts that need
 * no dial. Its engine is asked nothing until it is back.
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
      {/* Keyed on whether the server answers: a home that comes back starts
          its samples again rather than joining them across the gap. */}
      {answering && Home ? <Home key="answering" /> : <DownHome key="down" />}
    </SectionFrame>
  )
}

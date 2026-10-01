"use client"

import { useNavScope } from "@/components/nav-scope"
import { sectionHref } from "@/components/database/engine"
import { EngineGlyph } from "@/components/database/kit/engine-mark"
import { useDatabase } from "@/components/database/shell/database-context"
import { databaseNavGroups } from "@/components/database/shell/nav-groups"

/**
 * Hands the rail this database's panel for as long as its layout is mounted.
 *
 * The panel sits a level below Databases, the way a project sits below
 * Deployments, so the rail's back control leads to the section — the control
 * center, the map — rather than past it. Its rows keep the reader's place:
 * the table open in Data is the table Schema opens on.
 */
export function useDatabaseNavScope() {
  const { id, conn, engine, href } = useDatabase()
  useNavScope({
    path: sectionHref(id),
    replaces: false,
    title: conn.name,
    mark: <EngineGlyph engine={engine} className="size-4" />,
    groups: databaseNavGroups(engine.sections, href),
  })
}

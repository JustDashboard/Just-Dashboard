"use client"

import { useAuth } from "@/hooks/use-auth"
import { ErDiagram } from "@/components/database/diagram/er-diagram"
import { SectionFrame } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The schema as a diagram, for a SQL engine: the canvas, with the places a
 * table on it leads to wired to this database's own pages — its rows in Data,
 * its columns in Schema, a SELECT of it in Query.
 */
export function Diagram() {
  const { conn, selection, goto } = useDatabase()
  const { can } = useAuth()
  return (
    <SectionFrame section="diagram">
      <ErDiagram
        conn={conn}
        schema={selection.schema}
        canSave={can("service.control")}
        onOpenTable={(schema, table) => goto("data", { schema, table })}
        onOpenStructure={(schema, table) => goto("schema", { schema, table })}
        onQuery={(sql) => goto("query", { sql })}
      />
    </SectionFrame>
  )
}

import { get } from "@/lib/api"
import { inlineStatement } from "@/components/database/data/statement"

/** How many rows the statement handed to Query asks for. */
const FIRST = 100

/**
 * A SELECT of one table, as the server writes it for this engine.
 *
 * "The first hundred rows of a table" is spelled three ways across the
 * engines, and a name is quoted four. The page used to branch on the driver
 * and join the schema and the name with a dot, unquoted — wrong for a mixed-
 * case name, and one more copy of what each dialect is. The server already
 * knows: `GET /browse` answers with the statement it ran, in the engine's own
 * words with every name quoted, and a marker where the page size and the
 * offset went. One row is asked for, so the read costs nothing, and the
 * markers are filled with the size wanted.
 */
export async function selectStatement(
  id: number,
  schema: string,
  table: string,
  signal?: AbortSignal,
): Promise<string> {
  const page = await get<{ statement?: string }>(
    `/databases/${id}/browse`,
    { schema: schema || undefined, table, limit: 1 },
    signal,
  )
  const sql = page.statement
    ? inlineStatement(page.statement, [], { limit: FIRST, offset: 0 })
    : null
  if (!sql) throw new Error("The server did not say how it reads this table.")
  return sql
}

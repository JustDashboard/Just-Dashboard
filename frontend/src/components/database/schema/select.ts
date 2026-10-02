import type { Engine } from "@/components/database/engine"
import { notesFor, quotedRelation } from "@/components/database/schema/engine-notes"

/** How many rows the statement handed to Query asks for. */
const FIRST = 100

/**
 * A SELECT of one table or view, in the engine's own words: what "Query"
 * hands to the editor from the schema browser and from the diagram.
 *
 * "The first hundred rows" is spelled three ways across the engines and a
 * name is quoted three. The diagram used to branch on the driver and join the
 * schema and the name with a dot, unquoted — wrong for a mixed-case name. Its
 * successor asked the server, by reading a row of the relation and taking the
 * statement that read reported: right in every dialect, but it ran the
 * relation to learn a string, and a view that takes a minute to produce its
 * first row made the press wait a minute.
 *
 * So the statement is written here, at once and without a request, from what
 * the engine's notes say of its dialect. Both parts of the name are always
 * quoted, so a name is never folded to another case or read as a word of the
 * language.
 */
export function selectStatement(
  engine: Pick<Engine, "driver">,
  schema: string,
  table: string,
): string {
  const notes = notesFor(engine)
  return notes.firstRows(quotedRelation(schema, table, notes), FIRST)
}

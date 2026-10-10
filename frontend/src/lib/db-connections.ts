import type { DbConnection } from "@/lib/types"

/**
 * Why a saved connection cannot be used, or `undefined` when it can.
 *
 * `GET /databases/` lists a row whose connection string no longer opens, so
 * the Databases section can show it and let it be repaired or forgotten. Its
 * address fields are empty and every data route under its id fails, which
 * makes it a row every other page that picks from the list has to draw as
 * unusable rather than offer: a backup job that dumps it fails each night, and
 * a deployment handed its address is handed nothing. The words are the
 * section's own for the state ("cannot be opened").
 */
export function unusableReason(
  connection: Pick<DbConnection, "broken" | "brokenReason">,
): string | undefined {
  if (!connection.broken) return undefined
  return connection.brokenReason || "This saved connection cannot be opened."
}

/** How many of the saved connections cannot be opened. */
export function unusableCount(connections: Pick<DbConnection, "broken">[] | undefined): number {
  return (connections ?? []).filter((connection) => connection.broken).length
}

import { patchDsn, readDsn, type DsnParts } from "@/components/database/ops/settings-dsn"

/**
 * What the address fields on General hold against the saved string: the
 * string a save would write, whether anything differs, and how many fields
 * say so. No React.
 *
 * The page draws its fields before the saved string has been read: that read
 * is recorded on the server and is made only once a field changes. Until it
 * lands the fields show the connection's own row, and an edit is counted
 * against the row; afterwards it is counted against the string, which is what
 * a save replaces.
 */

export type AddressShape = "fields" | "whole"

/** What the fields hold that differs from what they showed. An absent key is untouched. */
export type AddressEdits = {
  host?: string
  port?: string
  user?: string
  password?: string
  database?: string
}

/** What the connection's row says of its address, which the page has without reading the string. */
export type AddressRow = { host?: string; port?: string; user?: string; database?: string }

const FIELDS = ["host", "port", "user", "database"] as const

export type AddressDraft = {
  /** The string the fields are laid over, taken apart; nothing until it has been read. */
  parts: DsnParts | undefined
  /** What the fields have made of that string. */
  fromFields: string
  /** What a save would write. Empty until the string has been read. */
  dsn: string
  dirty: boolean
  /** How many fields differ, for the save bar's count; one for a string typed whole. */
  changes: number
  /** The fields have emptied the string: there is nothing to save. */
  empty: boolean
}

export function addressDraft({
  stored,
  base,
  shape,
  edits,
  whole,
  row,
}: {
  /** The saved string, once read. */
  stored: string | undefined
  /** The string the fields are laid over: the saved one, until the whole string is retyped. */
  base: string | undefined
  shape: AddressShape
  edits: AddressEdits
  whole: string
  row: AddressRow
}): AddressDraft {
  const parts = base !== undefined ? readDsn(base) : undefined
  // An empty password field keeps the saved one; the other fields say what they hold.
  const change = { ...edits, password: edits.password ? edits.password : undefined }
  const fromFields = base !== undefined ? patchDsn(base, change) : ""
  const shown = (key: (typeof FIELDS)[number]) => (parts ? parts[key] : (row[key] ?? ""))
  const edited =
    FIELDS.filter((key) => edits[key] !== undefined && edits[key] !== shown(key)).length +
    (edits.password ? 1 : 0)
  if (shape === "whole") {
    const dsn = whole.trim()
    const dirty = dsn !== "" && dsn !== stored
    return { parts, fromFields, dsn, dirty, changes: dirty ? 1 : 0, empty: false }
  }
  const dirty = base === undefined ? edited > 0 : fromFields !== stored
  return {
    parts,
    fromFields,
    dsn: fromFields,
    dirty,
    changes: dirty ? Math.max(edited, 1) : 0,
    empty: dirty && base !== undefined && fromFields === "",
  }
}

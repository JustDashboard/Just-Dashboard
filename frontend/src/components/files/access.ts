export type Access = { read: boolean; write: boolean; run: boolean }
export type AccessRow = { who: "Owner" | "Group" | "Everyone"; name?: string; access: Access }

/**
 * A mode as the three rows a person reads it in — the owner, the group,
 * everyone else — each with what it may do. `0664` is a fact about who can
 * change a file, and the inspector used to print it as the number alone.
 */
export function accessRows(modeOctal: string, owner?: string, group?: string): AccessRow[] {
  const digits = (modeOctal || "0").replace(/\D/g, "").slice(-3).padStart(3, "0")
  const bits = digits.split("").map((d) => parseInt(d, 8) || 0)
  const access = (n: number): Access => ({
    read: (n & 4) !== 0,
    write: (n & 2) !== 0,
    run: (n & 1) !== 0,
  })
  return [
    { who: "Owner", name: owner, access: access(bits[0]) },
    { who: "Group", name: group, access: access(bits[1]) },
    { who: "Everyone", access: access(bits[2]) },
  ]
}

/**
 * The same rows as one line: who can change it, then who can only look.
 * A folder is opened and changed rather than read and edited, because that is
 * what its bits mean.
 */
export function accessSummary(rows: AccessRow[], isDir: boolean): string {
  const [owner, group, everyone] = rows
  const change = isDir ? "change" : "edit"
  const look = isDir ? "open" : "read"
  if (everyone.access.write) return `Anyone can ${change} this`
  const name = (row: AccessRow) =>
    row.who === "Group" ? `group ${row.name ?? "members"}` : (row.name ?? "the owner")
  const writers = [owner, group].filter((row) => row.access.write).map(name)
  const readers = everyone.access.read
    ? "everyone"
    : group.access.read && !group.access.write
      ? name(group)
      : null
  const parts: string[] = []
  if (writers.length) parts.push(`${writers.join(" and ")} can ${change}`)
  else parts.push(`No one can ${change} this`)
  if (readers) parts.push(`${readers} can ${look}`)
  else if (!group.access.read) parts.push(`only ${name(owner)} can ${look}`)
  return parts.join(" · ")
}

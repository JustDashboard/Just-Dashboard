/**
 * When a saved form takes effect, as the save bar says it.
 *
 * - `immediately`: the save is the change.
 * - `next-reload`: written now and live once nginx next reloads (the proxy's
 *   settings saved without a reload).
 * - `restart`: a file a running server reads only when it starts (a game's
 *   own settings file), written now and applied by its next restart.
 * - `next-deployment`: a project's configuration, live from the next
 *   deployment.
 *
 * A form whose save does something none of these says (it restarts the
 * dashboard, it signs the reader out) says so in its own words with `note`.
 */
export type SettingApplies = "next-deployment" | "immediately" | "next-reload" | "restart"

/** Soonest first: the order the bar names them in when a page holds several. */
const WHEN: [SettingApplies, string][] = [
  ["immediately", "immediately"],
  ["next-reload", "when nginx next reloads"],
  ["restart", "when the server restarts"],
  ["next-deployment", "on your next deployment"],
]

/**
 * The bar's second line for the forms it is about to save, or has saved.
 *
 * One kind is said whole: "Applies when the server restarts". Several are
 * split at the soonest, "Some apply now, the rest on your next deployment",
 * and what the rest wait for is every other kind on the page, so the line is
 * true for any mix of them and not only for the first two the bar knew.
 */
export function appliesLine(kinds: Iterable<SettingApplies>): string {
  const held = new Set(kinds)
  const [first, ...rest] = WHEN.filter(([kind]) => held.has(kind))
  if (!first) return "Applies immediately"
  if (rest.length === 0) return `Applies ${first[1]}`
  const soonest = first[0] === "immediately" ? "now" : first[1]
  return `Some apply ${soonest}, the rest ${rest.map(([, words]) => words).join(" or ")}`
}

/** The parts the bar is about, as a list in words: "General, Build and Runtime". */
export function namesLine(names: string[]): string {
  const list = [...new Set(names)]
  if (list.length <= 1) return list[0] ?? ""
  return `${list.slice(0, -1).join(", ")} and ${list.at(-1)}`
}

/**
 * The bar's second line while there is something to save.
 *
 * A value that cannot be saved is said before anything else: the line used
 * to give a form's `note` first, so a page with one never said why its Save
 * was disabled. A `note` then stands in for the kinds, since a form writes
 * one only where its save does something the reader must know before
 * pressing.
 */
export function pendingLine(
  entries: { name: string; applies: SettingApplies; note?: string }[],
  invalid: boolean,
): string {
  if (invalid) return "A value is out of range — fix it to save"
  const note = entries.find((entry) => entry.note)?.note
  const names = namesLine(entries.map((entry) => entry.name))
  return `${names} · ${note ?? appliesLine(entries.map((entry) => entry.applies))}`
}

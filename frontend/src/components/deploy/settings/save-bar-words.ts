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
 * A form that gives a `note` speaks for itself, and for why it cannot be
 * saved too: the account's password form says "The new passwords do not
 * match yet" there, which is more use than the line every other form gets.
 * So a note comes first. Without one, a value that cannot be saved is said
 * in the bar's own words, and otherwise the line is when the saves apply.
 */
export function pendingLine(
  entries: { name: string; applies: SettingApplies; note?: string }[],
  invalid: boolean,
): string {
  const note = entries.find((entry) => entry.note)?.note
  const names = namesLine(entries.map((entry) => entry.name))
  if (note) return `${names} · ${note}`
  if (invalid) return "A value is out of range — fix it to save"
  return `${names} · ${appliesLine(entries.map((entry) => entry.applies))}`
}

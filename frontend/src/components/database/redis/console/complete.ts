import type { RedisCommandRef } from "@/components/database/redis/types"

/**
 * What the console knows about a line before it is sent: which command it
 * names, and which commands the letters typed so far could become.
 *
 * The names are the server's own (`GET /redis/commands`), so the list is
 * right for the product that answered — Valkey's, Dragonfly's, a loaded
 * module's — and a two-word command (`CONFIG SET`, `XINFO STREAM`) is one
 * name here, as it is there.
 */

/** The words of a line up to its arguments: at most two, upper-cased, as a command is named. */
function head(line: string): { words: string[]; open: boolean } {
  const text = line.replace(/^\s+/, "")
  const words = text.split(/\s+/).filter(Boolean)
  // "Open" while the caret is still in the last word: nothing has ended it.
  return { words: words.map((word) => word.toUpperCase()), open: !/\s$/.test(text) }
}

/**
 * The command a line names, once enough of it is typed to say: the two-word
 * name where the first two words are one (`CONFIG GET`), else the first word.
 */
export function commandOf(
  line: string,
  commands: readonly RedisCommandRef[],
): RedisCommandRef | undefined {
  const { words, open } = head(line)
  if (words.length === 0) return undefined
  const two = words.length >= 2 ? `${words[0]} ${words[1]}` : undefined
  // A word still being typed names nothing yet: `SE` is not `SET`.
  const settled = (count: number) => words.length > count || !open
  if (two && settled(2)) {
    const pair = commands.find((command) => command.name === two)
    if (pair) return pair
  }
  // `CONFIG` alone is no command, only its subcommands are: it names nothing
  // until the second word is typed.
  return settled(1) ? commands.find((command) => command.name === words[0]) : undefined
}

/**
 * The commands the typing so far could become, best first: the name itself,
 * then names that start with it, shortest first. Nothing once the line has
 * moved on to arguments.
 */
export function completions(
  line: string,
  commands: readonly RedisCommandRef[],
  limit = 8,
): RedisCommandRef[] {
  const { words, open } = head(line)
  if (words.length === 0 || words.length > 2) return []
  // After a whole first word and a space, only that family's subcommands fit.
  const typed = open ? words.join(" ") : `${words.join(" ")} `
  if (words.length === 2 && !open) return []
  const found = commands.filter(
    (command) => command.name.startsWith(typed) && command.name !== typed.trimEnd(),
  )
  // A second word that matches no subcommand is an argument, not a name.
  if (words.length === 2 && found.length === 0) return []
  return found
    .sort((a, b) => a.name.length - b.name.length || a.name.localeCompare(b.name))
    .slice(0, limit)
}

/**
 * The line once a completion is picked: the command's name, ready for its
 * arguments. A completion is only offered while the name is being typed, so
 * there is nothing after it to keep.
 */
export function accept(command: RedisCommandRef): string {
  return `${command.name} `
}

/** History with a line added: the newest last, the same line not twice in a row, capped. */
export function remember(history: readonly string[], line: string, cap = 200): string[] {
  const typed = line.trim()
  if (!typed || history[history.length - 1] === typed) return [...history]
  return [...history, typed].slice(-cap)
}

/** Commands grouped as the reference pane lists them: by the server's group, each in name order. */
export function groupCommands(
  commands: readonly RedisCommandRef[],
  query = "",
): { group: string; commands: RedisCommandRef[] }[] {
  const wanted = query.trim().toUpperCase()
  const groups = new Map<string, RedisCommandRef[]>()
  for (const command of commands) {
    if (
      wanted &&
      !command.name.includes(wanted) &&
      !(command.summary ?? "").toUpperCase().includes(wanted)
    ) {
      continue
    }
    const group = command.group || "other"
    groups.set(group, [...(groups.get(group) ?? []), command])
  }
  return [...groups.entries()]
    .map(([group, list]) => ({
      group,
      commands: list.sort((a, b) => a.name.localeCompare(b.name)),
    }))
    .sort((a, b) => a.group.localeCompare(b.group))
}

import type { BlueprintProperty } from "@/lib/types"

/**
 * How a game's settings file is read as a page: which declared keys stand
 * together, and what each group currently is in a few words.
 *
 * A blueprint declares its keys as one flat list in the order the file's
 * documentation gives them, so the page drew one section of every field. A
 * reader looks for a setting by what it is about — the world, who may join,
 * how the server is found — and those are the sections here. A key this file
 * does not know lands in the last group rather than being dropped, so a new
 * blueprint's settings are all editable before anybody sorts them.
 */

export type PropertyGroupId = "world" | "players" | "network" | "more"

export type PropertyGroup = {
  id: PropertyGroupId
  title: string
  properties: BlueprintProperty[]
}

const TITLES: Record<PropertyGroupId, string> = {
  world: "World",
  players: "Players",
  network: "Network",
  more: "More",
}

const ORDER: PropertyGroupId[] = ["world", "players", "network", "more"]

/** The keys both Minecraft editions write, and the ones their forks add. */
const GROUP_OF: Record<string, PropertyGroupId> = {
  difficulty: "world",
  gamemode: "world",
  "force-gamemode": "world",
  hardcore: "world",
  pvp: "world",
  "view-distance": "world",
  "simulation-distance": "world",
  "tick-distance": "world",
  "spawn-protection": "world",
  "level-name": "world",
  "level-seed": "world",
  "level-type": "world",
  "allow-nether": "world",
  "allow-flight": "world",
  "allow-cheats": "world",
  "spawn-monsters": "world",
  "spawn-animals": "world",
  "spawn-npcs": "world",
  "generate-structures": "world",
  "max-world-size": "world",

  "max-players": "players",
  "white-list": "players",
  "enforce-whitelist": "players",
  "allow-list": "players",
  "online-mode": "players",
  "player-idle-timeout": "players",
  "op-permission-level": "players",
  "default-player-permission-level": "players",

  motd: "network",
  "server-name": "network",
  "server-port": "network",
  "server-portv6": "network",
  "server-ip": "network",
  "enable-status": "network",
  "enable-query": "network",
  "enable-rcon": "network",
  "enable-lan-visibility": "network",
  "network-compression-threshold": "network",
}

/** The declared keys in their sections, each section in the blueprint's own order. */
export function groupProperties(known: BlueprintProperty[]): PropertyGroup[] {
  const groups = new Map<PropertyGroupId, BlueprintProperty[]>()
  for (const property of known) {
    const id = GROUP_OF[property.key] ?? "more"
    groups.set(id, [...(groups.get(id) ?? []), property])
  }
  return ORDER.filter((id) => groups.has(id)).map((id) => ({
    id,
    title: TITLES[id],
    properties: groups.get(id)!,
  }))
}

const on = (value: string) => value === "true"

/** What a key's value reads as in a facts line, where its label would not do. */
const FACT: Record<string, (value: string) => string | undefined> = {
  "max-players": (value) => `${value} ${value === "1" ? "player" : "players"}`,
  "view-distance": (value) => `view ${value} chunks`,
  "simulation-distance": (value) => `simulates ${value} chunks`,
  "spawn-protection": (value) =>
    value === "0" ? "spawn unprotected" : `spawn protected ${value} blocks`,
  pvp: (value) => (on(value) ? "PvP on" : "PvP off"),
  hardcore: (value) => (on(value) ? "hardcore" : undefined),
  "white-list": (value) => (on(value) ? "whitelist only" : "open to anyone"),
  "allow-list": (value) => (on(value) ? "allow list only" : "open to anyone"),
  "online-mode": (value) => (on(value) ? "accounts verified" : "accounts not verified"),
}

/** How many facts a section's head holds: a line, not a second form. */
const FACTS = 3

/**
 * What a section currently is, as the few words under its title: "Hard · PvP
 * off · view 10 chunks". Read from `value`, which answers with the draft, so
 * the line says what saving would write. A key with nothing to say — an empty
 * line of text, a switch that is off and has no word for off — is left out.
 */
export function groupFacts(
  properties: BlueprintProperty[],
  value: (property: BlueprintProperty) => string,
): string[] {
  const facts: string[] = []
  for (const property of properties) {
    if (facts.length === FACTS) break
    const current = value(property).trim()
    if (current === "") continue
    const known = FACT[property.key]
    const fact = known
      ? known(current)
      : property.kind === "choice"
        ? (property.choices?.find((choice) => choice.value === current)?.label ?? current)
        : property.kind === "boolean"
          ? on(current)
            ? property.label
            : undefined
          : property.kind === "number"
            ? `${property.label.toLowerCase()} ${current}`
            : current
    if (fact) facts.push(fact)
  }
  return facts
}

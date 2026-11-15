import { expect, test } from "bun:test"
import { groupFacts, groupProperties } from "./settings-model"

const java = [
  { key: "motd", kind: "text", label: "Server list description" },
  { key: "max-players", kind: "number", label: "Maximum players", minimum: 1, maximum: 1000 },
  {
    key: "difficulty",
    kind: "choice",
    label: "Difficulty",
    choices: [
      { value: "easy", label: "Easy" },
      { value: "hard", label: "Hard" },
    ],
  },
  { key: "pvp", kind: "boolean", label: "Player versus player" },
  { key: "white-list", kind: "boolean", label: "Whitelist only" },
  { key: "online-mode", kind: "boolean", label: "Verify accounts with Mojang" },
  { key: "view-distance", kind: "number", label: "View distance", minimum: 3, maximum: 32 },
  { key: "simulation-distance", kind: "number", label: "Simulation distance" },
  { key: "spawn-protection", kind: "number", label: "Spawn protection radius" },
]

test("the declared keys stand in World, Players and Network, each in the blueprint's order", () => {
  const groups = groupProperties(java)
  expect(groups.map((group) => group.title)).toEqual(["World", "Players", "Network"])
  expect(groups[0].properties.map((property) => property.key)).toEqual([
    "difficulty",
    "pvp",
    "view-distance",
    "simulation-distance",
    "spawn-protection",
  ])
  expect(groups[1].properties.map((property) => property.key)).toEqual([
    "max-players",
    "white-list",
    "online-mode",
  ])
  expect(groups[2].properties.map((property) => property.key)).toEqual(["motd"])
})

test("a key nobody sorted is still on the page, in a last section", () => {
  const groups = groupProperties([
    { key: "motd", kind: "text", label: "Server list description" },
    { key: "resource-pack", kind: "text", label: "Resource pack" },
  ])
  expect(groups.map((group) => group.id)).toEqual(["network", "more"])
  expect(groups[1].properties[0].key).toBe("resource-pack")
  expect(groupProperties([])).toEqual([])
})

test("a section's head says what it is in at most three facts", () => {
  const values = {
    difficulty: "hard",
    pvp: "false",
    "view-distance": "10",
    "simulation-distance": "8",
    "max-players": "20",
    "white-list": "true",
    "online-mode": "true",
    motd: "A quiet server",
  }
  const value = (property) => values[property.key] ?? ""
  const [world, players, network] = groupProperties(java)
  expect(groupFacts(world.properties, value)).toEqual(["Hard", "PvP off", "view 10 chunks"])
  expect(groupFacts(players.properties, value)).toEqual([
    "20 players",
    "whitelist only",
    "accounts verified",
  ])
  expect(groupFacts(network.properties, value)).toEqual(["A quiet server"])
})

test("a value with nothing to say is left out of the line", () => {
  const value = (property) =>
    ({ motd: "  ", "max-players": "1", flag: "false" })[property.key] ?? ""
  expect(groupFacts([{ key: "motd", kind: "text", label: "Description" }], value)).toEqual([])
  expect(groupFacts([{ key: "max-players", kind: "number", label: "Maximum" }], value)).toEqual([
    "1 player",
  ])
  // A switch this file has no word for is named when on and silent when off.
  expect(groupFacts([{ key: "flag", kind: "boolean", label: "Flight" }], value)).toEqual([])
  expect(groupFacts([{ key: "flag", kind: "boolean", label: "Flight" }], () => "true")).toEqual([
    "Flight",
  ])
  expect(groupFacts([{ key: "tick", kind: "number", label: "Tick rate" }], () => "20")).toEqual([
    "tick rate 20",
  ])
})

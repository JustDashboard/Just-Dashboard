import { describe, expect, test } from "bun:test"
import { accept, commandOf, completions, groupCommands, remember } from "./complete"
import { listenTargets } from "./targets"

const ref = (name, group = "generic", summary = "") => ({
  name,
  group,
  summary,
  arity: -2,
  flags: [],
  categories: [],
  class: "read",
  admin: false,
})
const COMMANDS = [
  ref("GET", "string", "Returns the string value of a key."),
  ref("GETDEL", "string"),
  ref("GETRANGE", "string"),
  ref("HGET", "hash"),
  ref("HGETALL", "hash", "Returns all fields and values in a hash."),
  ref("SET", "string"),
  ref("CONFIG GET", "server"),
  ref("CONFIG SET", "server"),
  ref("XINFO STREAM", "stream"),
]
const names = (list) => list.map((command) => command.name)

describe("which command a line names", () => {
  test("nothing until the name is finished", () => {
    expect(commandOf("", COMMANDS)).toBeUndefined()
    expect(commandOf("ge", COMMANDS)).toBeUndefined()
    // `GET` typed in full with nothing after it is still being typed: it may
    // be about to become GETDEL.
    expect(commandOf("get", COMMANDS)).toBeUndefined()
  })

  test("the command, once a space ends its name, whatever the case", () => {
    expect(commandOf("get ", COMMANDS)?.name).toBe("GET")
    expect(commandOf("  Get user:1", COMMANDS)?.name).toBe("GET")
    expect(commandOf("SET k v EX 10", COMMANDS)?.name).toBe("SET")
  })

  test("a two-word command is one name", () => {
    expect(commandOf("config get maxmemory", COMMANDS)?.name).toBe("CONFIG GET")
    expect(commandOf("CONFIG SET ", COMMANDS)?.name).toBe("CONFIG SET")
    // The family alone names no command.
    expect(commandOf("config ", COMMANDS)).toBeUndefined()
    expect(commandOf("config ge", COMMANDS)).toBeUndefined()
  })

  test("an argument that happens to be a word is not a subcommand", () => {
    expect(commandOf("GET set", COMMANDS)?.name).toBe("GET")
  })

  test("a command nobody knows names nothing", () => {
    expect(commandOf("FROB x", COMMANDS)).toBeUndefined()
  })
})

describe("what the typing could become", () => {
  test("names that start with it, shortest first", () => {
    expect(names(completions("ge", COMMANDS))).toEqual(["GET", "GETDEL", "GETRANGE"])
    expect(names(completions("hg", COMMANDS))).toEqual(["HGET", "HGETALL"])
  })

  test("a name typed in full offers only what is longer", () => {
    expect(names(completions("GET", COMMANDS))).toEqual(["GETDEL", "GETRANGE"])
    expect(names(completions("SET", COMMANDS))).toEqual([])
  })

  test("a family's subcommands, once its first word is typed", () => {
    expect(names(completions("config", COMMANDS))).toEqual(["CONFIG GET", "CONFIG SET"])
    expect(names(completions("config ", COMMANDS))).toEqual(["CONFIG GET", "CONFIG SET"])
    expect(names(completions("config s", COMMANDS))).toEqual(["CONFIG SET"])
  })

  test("nothing once the line has moved on to arguments", () => {
    expect(completions("get user:1", COMMANDS)).toEqual([])
    expect(completions("get ", COMMANDS)).toEqual([])
    expect(completions("config get max", COMMANDS)).toEqual([])
    expect(completions("", COMMANDS)).toEqual([])
  })

  test("no more than asked for", () => {
    expect(completions("g", COMMANDS, 2).length).toBe(2)
  })

  test("a completion picked is the name, ready for its arguments", () => {
    expect(accept(COMMANDS[4])).toBe("HGETALL ")
    expect(accept(COMMANDS[6])).toBe("CONFIG GET ")
  })
})

describe("history", () => {
  test("the newest last; the same line twice in a row is one entry", () => {
    expect(remember(["PING"], "GET a")).toEqual(["PING", "GET a"])
    expect(remember(["PING"], " PING ")).toEqual(["PING"])
    expect(remember(["PING", "GET a"], "PING")).toEqual(["PING", "GET a", "PING"])
  })

  test("an empty line is not history, and the list is capped", () => {
    expect(remember(["PING"], "   ")).toEqual(["PING"])
    expect(remember(["a", "b", "c"], "d", 3)).toEqual(["b", "c", "d"])
  })
})

describe("the reference pane", () => {
  test("groups by the server's group, in name order", () => {
    const groups = groupCommands(COMMANDS)
    expect(groups.map((entry) => entry.group)).toEqual(["hash", "server", "stream", "string"])
    expect(names(groups[3].commands)).toEqual(["GET", "GETDEL", "GETRANGE", "SET"])
  })

  test("a search matches names and summaries, and drops empty groups", () => {
    expect(groupCommands(COMMANDS, "getall").map((entry) => entry.group)).toEqual(["hash"])
    expect(names(groupCommands(COMMANDS, "string value")[0].commands)).toEqual(["GET"])
    expect(groupCommands(COMMANDS, "zzz")).toEqual([])
  })

  test("a command with no group is listed under other", () => {
    expect(groupCommands([{ ...ref("X"), group: undefined }])[0].group).toBe("other")
  })
})

describe("what a feed listens to", () => {
  test("a word is a channel and a glob is a pattern", () => {
    expect(listenTargets("orders.* news, alerts?")).toEqual({
      channel: ["news"],
      pattern: ["orders.*", "alerts?"],
    })
    expect(listenTargets("*")).toEqual({ channel: [], pattern: ["*"] })
    expect(listenTargets("  ")).toEqual({ channel: [], pattern: [] })
  })

  test("no more than the server takes of each", () => {
    const many = Array.from({ length: 30 }, (_, i) => `c${i}`).join(" ")
    expect(listenTargets(many).channel.length).toBe(20)
  })
})

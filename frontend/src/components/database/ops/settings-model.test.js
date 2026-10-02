import { describe, expect, test } from "bun:test"
import {
  connectionChanges,
  draftOf,
  dropEffect,
  dropPhrase,
  firewallOutcome,
  firewallWords,
  notesProblem,
  reachRefusal,
} from "./settings-model"

const SQL = { fileBased: false, numbered: false, documents: false }
const FILE = { fileBased: true, numbered: false, documents: false }
const NUMBERED = { fileBased: false, numbered: true, documents: false }
const DOCUMENTS = { fileBased: false, numbered: false, documents: true }

describe("what a save of the connection's labels sends", () => {
  const saved = { name: "shop", environment: "production", notes: "", readOnly: false }

  test("a connection's labels become a draft, with what is absent as empty", () => {
    expect(draftOf({ name: "shop" })).toEqual({
      name: "shop",
      environment: "",
      notes: "",
      readOnly: false,
    })
  })

  test("only the fields that changed are sent", () => {
    expect(connectionChanges(saved, saved)).toEqual({})
    expect(connectionChanges(saved, { ...saved, notes: "the shop" })).toEqual({ notes: "the shop" })
    expect(connectionChanges(saved, { ...saved, readOnly: true })).toEqual({ readOnly: true })
  })

  test("an environment taken away is sent as empty, which clears it", () => {
    expect(connectionChanges(saved, { ...saved, environment: "  " })).toEqual({ environment: "" })
  })

  test("a name is compared without the spaces round it", () => {
    expect(connectionChanges(saved, { ...saved, name: " shop " })).toEqual({})
    expect(connectionChanges(saved, { ...saved, name: "shop-eu" })).toEqual({ name: "shop-eu" })
  })

  test("notes are held to the bytes the server keeps, not the characters", () => {
    expect(notesProblem("a".repeat(4000))).toBeUndefined()
    expect(notesProblem("a".repeat(4001))).toBeDefined()
    // Two bytes each.
    expect(notesProblem("ă".repeat(2001))).toBeDefined()
  })
})

describe("the phrase a database's deletion is typed with", () => {
  test("a server's database is typed by its name", () => {
    expect(dropPhrase({ database: "shop_main" }, SQL)).toBe("shop_main")
    expect(dropPhrase({ database: "app" }, DOCUMENTS)).toBe("app")
  })

  test("a file is typed by its name, not its path", () => {
    expect(dropPhrase({ database: "/srv/notes/notes.db" }, FILE)).toBe("notes.db")
  })

  test("a numbered database is typed the way the engine writes it", () => {
    expect(dropPhrase({ database: "9" }, NUMBERED)).toBe("db9")
    expect(dropPhrase({ database: "db3" }, NUMBERED)).toBe("db3")
    // A connection that names no number is on database 0.
    expect(dropPhrase({ database: "" }, NUMBERED)).toBe("db0")
  })

  test("a connection that names no database has no phrase: there is nothing to delete", () => {
    expect(dropPhrase({ database: "" }, SQL)).toBe("")
    expect(dropPhrase({ database: "  " }, DOCUMENTS)).toBe("")
  })

  test("each kind of engine loses something different", () => {
    expect(dropEffect(SQL, "row")).toContain("table")
    expect(dropEffect(DOCUMENTS, "document")).toContain("collection")
    expect(dropEffect(FILE, "row")).toContain("file is deleted")
    expect(dropEffect(NUMBERED, "key")).toContain("Every key")
    expect(dropEffect(NUMBERED, "key")).toContain("connection keeps working")
  })
})

describe("why where a server listens is not changed from the page", () => {
  const firewall = { active: false, open: false, editable: false }

  test("a container the dashboard owns can be changed: nothing is in the way", () => {
    expect(reachRefusal({ managed: true, exposure: "local", container: "shop-db" })).toBeUndefined()
  })

  test("a server on another machine is changed there", () => {
    expect(reachRefusal({ managed: false, exposure: "remote" })).toContain("another machine")
  })

  test("a compose-owned container is changed in its compose file, whatever else is true", () => {
    expect(
      reachRefusal({
        managed: false,
        exposure: "public",
        container: "stack-db-1",
        composeProject: "stack",
      }),
    ).toContain("compose project stack")
  })

  test("a binding to one chosen address, a container with no port, and a process on the host", () => {
    expect(reachRefusal({ managed: false, exposure: "private", container: "c" })).toContain(
      "one chosen address",
    )
    expect(reachRefusal({ managed: false, exposure: "local", container: "c" })).toContain(
      "publishes no port",
    )
    expect(reachRefusal({ managed: false, exposure: "local" })).toContain("own configuration")
  })

  test("the firewall is said as it is: absent, off, or on with the port open or closed", () => {
    expect(firewallWords({ firewall, port: 5432 })).toContain("No firewall")
    expect(firewallWords({ firewall: { ...firewall, backend: "ufw" }, port: 5432 })).toContain(
      "switched off",
    )
    expect(
      firewallWords({ firewall: { backend: "ufw", active: true, open: false }, port: 5432 }),
    ).toBe("ufw is on, and port 5432 is closed.")
    expect(
      firewallWords({ firewall: { backend: "ufw", active: true, open: true }, port: 5432 }),
    ).toBe("ufw is on, and port 5432 is open to anywhere.")
  })

  test("what a change did to the firewall is one sentence per word, and none for a word nobody knows", () => {
    expect(firewallOutcome("opened", 5432)).toContain("5432")
    expect(firewallOutcome("closed", 5432)).toContain("removed")
    expect(firewallOutcome("already", 5432)).toContain("already")
    expect(firewallOutcome("none", 5432)).toContain("no firewall")
    expect(firewallOutcome("something-new", 5432)).toBe("")
  })
})

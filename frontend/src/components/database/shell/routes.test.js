import { describe, expect, test } from "bun:test"
import { databaseIdFrom, databasePlace, legacyDatabasesHref, newDatabaseHref } from "./routes"

const legacy = (address) => {
  const url = new URL(address, "http://localhost")
  return legacyDatabasesHref(url.pathname, url.searchParams)
}

describe("addresses from before the connection moved into the path", () => {
  test("each old page lands on the page that took its work", () => {
    expect(legacy("/databases/overview?conn=3")).toBe("/databases/3")
    expect(legacy("/databases/browse?conn=3")).toBe("/databases/3/data")
    expect(legacy("/databases/structure?conn=3")).toBe("/databases/3/schema")
    expect(legacy("/databases/diagram?conn=3")).toBe("/databases/3/diagram")
    expect(legacy("/databases/query?conn=3")).toBe("/databases/3/query")
    expect(legacy("/databases/find?conn=3")).toBe("/databases/3/search")
    expect(legacy("/databases/monitor?conn=3")).toBe("/databases/3/performance")
    expect(legacy("/databases/advisor?conn=3")).toBe("/databases/3/advisor")
    expect(legacy("/databases/server?conn=3")).toBe("/databases/3/access")
    expect(legacy("/databases/backups?conn=3")).toBe("/databases/3/backups")
    expect(legacy("/databases/logs?conn=3")).toBe("/databases/3/logs")
    expect(legacy("/databases/generate?conn=3")).toBe("/databases/3/generate")
    expect(legacy("/databases/connection?conn=3")).toBe("/databases/3/settings")
  })

  test("the map kept its picture and changed its name", () => {
    expect(legacy("/databases/topology")).toBe("/databases/map")
    expect(legacy("/databases/topology?conn=3")).toBe("/databases/map")
  })

  test("what the address said beyond the connection is carried over", () => {
    expect(legacy("/databases/browse?conn=1&schema=public&table=orders")).toBe(
      "/databases/1/data?schema=public&table=orders",
    )
    expect(legacy("/databases/structure?conn=1&schema=sales")).toBe(
      "/databases/1/schema?schema=sales",
    )
    expect(legacy("/databases/query?conn=2&sql=SELECT%20*%20FROM%20orders%3B")).toBe(
      "/databases/2/query?sql=SELECT+*+FROM+orders%3B",
    )
    expect(legacy("/databases/logs?conn=1&view=queries&source=docker%3Ashop-db")).toBe(
      "/databases/1/logs?source=docker%3Ashop-db&view=queries",
    )
  })

  test("a link stored in a board opens the database it was drawn for", () => {
    expect(legacy("/databases/overview?conn=42")).toBe("/databases/42")
  })

  test("an old page with no connection named opens the control center, not a guess", () => {
    expect(legacy("/databases/browse")).toBe("/databases")
    expect(legacy("/databases/browse?conn=")).toBe("/databases")
    expect(legacy("/databases/browse?conn=abc")).toBe("/databases")
    expect(legacy("/databases/browse?conn=0")).toBe("/databases")
    expect(legacy("/databases/connection?conn=-4")).toBe("/databases")
  })

  test("anything else is not an old address", () => {
    expect(legacy("/databases")).toBeNull()
    expect(legacy("/databases/3")).toBeNull()
    expect(legacy("/databases/3/data")).toBeNull()
    expect(legacy("/databases/new")).toBeNull()
    expect(legacy("/databases/map")).toBeNull()
    expect(legacy("/databases/nonsense?conn=3")).toBeNull()
    expect(legacy("/databases/browse/extra?conn=3")).toBeNull()
    // A name every object answers to is not one of the old pages.
    expect(legacy("/databases/constructor?conn=3")).toBeNull()
    expect(legacy("/databases/toString?conn=3")).toBeNull()
  })
})

describe("reading an address", () => {
  test("the database a path is inside", () => {
    expect(databaseIdFrom("/databases/12")).toBe(12)
    expect(databaseIdFrom("/databases/12/data")).toBe(12)
    expect(databaseIdFrom("/databases/12/anything/else")).toBe(12)
    expect(databaseIdFrom("/databases")).toBeNull()
    expect(databaseIdFrom("/databases/new")).toBeNull()
    expect(databaseIdFrom("/databases/browse")).toBeNull()
    expect(databaseIdFrom("/deploy/12")).toBeNull()
  })

  test("an id is spelled one way", () => {
    // The layout says "Database not found" for these; the rail must not
    // draw database 7's pages beside it.
    expect(databaseIdFrom("/databases/007/data")).toBeNull()
    expect(databaseIdFrom("/databases/0")).toBeNull()
    expect(databaseIdFrom("/databases/7x/data")).toBeNull()
    expect(databasePlace("/databases/007/data")).toBeNull()
    expect(databasePlace("/databases/0")).toBeNull()
  })

  test("the page of it", () => {
    expect(databasePlace("/databases/12")).toEqual({ id: 12, section: "home" })
    expect(databasePlace("/databases/12/")).toEqual({ id: 12, section: "home" })
    expect(databasePlace("/databases/12/query")).toEqual({ id: 12, section: "query" })
    expect(databasePlace("/databases/12/home")).toBeNull()
    expect(databasePlace("/databases/12/nonsense")).toBeNull()
    expect(databasePlace("/databases/new")).toBeNull()
  })
})

describe("the way to add a database", () => {
  test("opens bare, on a mode, or on one found server", () => {
    expect(newDatabaseHref()).toBe("/databases/new")
    expect(newDatabaseHref({ mode: "connect" })).toBe("/databases/new?mode=connect")
    expect(newDatabaseHref({ mode: "found", key: "tcp:127.0.0.1:5432" })).toBe(
      "/databases/new?mode=found&key=tcp%3A127.0.0.1%3A5432",
    )
  })
})

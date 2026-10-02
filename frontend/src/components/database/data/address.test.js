import { describe, expect, test } from "bun:test"
import {
  FLIGHT_MS,
  SETTLED,
  addressKey,
  addressOf,
  addressParams,
  landed,
  written,
  wrote,
} from "./address"

const at = (query) => addressOf((name) => new URLSearchParams(query).get(name) ?? "")
const key = (query) => addressKey(at(query))

const TABLE = "schema=public&table=orders"
const STRUCTURE = `${TABLE}&view=structure`
const SORTED = `${TABLE}&sort=x`
const SORTED_DESC = `${TABLE}&sort=y`

describe("the page's keys of the address", () => {
  test("only the page's own keys are read, and an absent one is empty", () => {
    expect(at("schema=public&table=orders&sql=select+1&page=3")).toEqual({
      schema: "public",
      table: "orders",
      view: "",
      filters: "",
      where: "",
      match: "",
      sort: "",
      page: "3",
    })
  })
  test("a write clears what it names null or empty and leaves the rest", () => {
    const next = written(at(`${STRUCTURE}&page=2`), { view: null, page: "", match: "any" })
    expect(next).toEqual(at(`${TABLE}&match=any`))
  })
  test("a key the page does not own is not taken into its address", () => {
    expect(written(at(TABLE), { sql: "select 1" })).toEqual(at(TABLE))
  })
  test("an address is the write that leads to it, empty keys cleared", () => {
    expect(addressParams(at(STRUCTURE))).toEqual({
      schema: "public",
      table: "orders",
      view: "structure",
      filters: null,
      where: null,
      match: null,
      sort: null,
      page: null,
    })
  })
})

describe("a write that the router has not shown yet", () => {
  test("is remembered with where it leads", () => {
    const flight = wrote(SETTLED, at(TABLE), key(TABLE), { view: "structure" }, 0)
    expect(flight.want).toEqual(at(STRUCTURE))
    expect(flight.flying).toEqual([key(STRUCTURE)])
  })
  test("and is done with once the router shows it", () => {
    const flight = wrote(SETTLED, at(TABLE), key(TABLE), { view: "structure" }, 0)
    expect(landed(flight, key(STRUCTURE), 5)).toEqual({ flight: SETTLED, rewrite: null })
  })
  test("a write of nothing, with nothing on its way, is not waited for", () => {
    expect(wrote(SETTLED, at(TABLE), key(TABLE), {}, 0)).toEqual(SETTLED)
  })
  test("writes made together build on each other, not on the address that was drawn", () => {
    const first = wrote(SETTLED, at(TABLE), key(TABLE), { sort: "x" }, 0)
    const second = wrote(first, at(TABLE), key(TABLE), { page: "2" }, 0)
    expect(second.want).toEqual(at(`${SORTED}&page=2`))
  })
})

describe("two writes made before the first has landed", () => {
  test("Structure then Data: the late landing on Structure is written over with Data", () => {
    const first = wrote(SETTLED, at(TABLE), key(TABLE), { view: "structure" }, 0)
    // The second write asks for the address the router still shows: nothing is sent for it.
    const second = wrote(first, at(STRUCTURE), key(TABLE), { view: null }, 100)
    expect(second.want).toEqual(at(TABLE))
    const late = landed(second, key(STRUCTURE), 400)
    expect(late.rewrite).toEqual(at(TABLE))
    // The rewrite reaches the router: the page is where the reader left it.
    expect(landed(late.flight, key(TABLE), 600)).toEqual({ flight: SETTLED, rewrite: null })
  })
  test("a filter applied and removed at once ends without it", () => {
    const filtered = `${TABLE}&filters=f`
    const first = wrote(SETTLED, at(TABLE), key(TABLE), { filters: "f", page: null }, 0)
    const second = wrote(first, at(filtered), key(TABLE), { filters: null, where: null }, 50)
    expect(landed(second, key(filtered), 1500).rewrite).toEqual(at(TABLE))
  })
  test("sort twice: the first landing is passed through to the second", () => {
    const first = wrote(SETTLED, at(TABLE), key(TABLE), { sort: "x" }, 0)
    const second = wrote(first, at(SORTED), key(TABLE), { sort: "y" }, 100)
    const passing = landed(second, key(SORTED), 300)
    expect(passing.rewrite).toEqual(at(SORTED_DESC))
    expect(landed(passing.flight, key(SORTED_DESC), 500)).toEqual({
      flight: SETTLED,
      rewrite: null,
    })
  })
})

describe("an address the page did not write", () => {
  test("a link to another table while a write is on its way is followed", () => {
    const flight = wrote(SETTLED, at(TABLE), key(TABLE), { view: "structure" }, 0)
    const elsewhere = key("schema=public&table=customers")
    expect(landed(flight, elsewhere, 200)).toEqual({ flight: SETTLED, rewrite: null })
  })
  test("with nothing on its way, any address is followed", () => {
    expect(landed(SETTLED, key(STRUCTURE), 0)).toEqual({ flight: SETTLED, rewrite: null })
  })
  test("a write the router never showed is forgotten, so Back to that address stays there", () => {
    const first = wrote(SETTLED, at(TABLE), key(TABLE), { view: "structure" }, 0)
    const second = wrote(first, at(STRUCTURE), key(TABLE), { view: null }, 100)
    const later = 100 + FLIGHT_MS + 1
    expect(landed(second, key(STRUCTURE), later)).toEqual({ flight: SETTLED, rewrite: null })
    // And a write made after that starts from the address as it is drawn.
    const fresh = wrote(second, at(STRUCTURE), key(STRUCTURE), { page: "2" }, later)
    expect(fresh.want).toEqual(at(`${STRUCTURE}&page=2`))
    expect(fresh.flying).toEqual([key(`${STRUCTURE}&page=2`)])
  })
})

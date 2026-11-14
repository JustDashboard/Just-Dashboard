import { expect, test } from "bun:test"
import { observePlayers, sameRecord } from "./players-model"

const at = (seconds) => new Date(Date.UTC(2026, 8, 3, 12, 0, seconds)).toISOString()

test("players already on at the first answer were not seen to arrive", () => {
  const record = observePlayers(undefined, ["Notch", "jeb_"], at(0))
  expect(record).toEqual({
    observedAt: at(0),
    players: {
      Notch: { at: at(0), arrived: false },
      jeb_: { at: at(0), arrived: false },
    },
  })
})

test("a name that turns up in the next answer joined between the two", () => {
  const first = observePlayers(undefined, ["Notch"], at(0))
  const second = observePlayers(first, ["Notch", "Dinnerbone"], at(20))
  expect(second.players.Notch).toEqual({ at: at(0), arrived: false })
  expect(second.players.Dinnerbone).toEqual({ at: at(20), arrived: true })
})

test("a player who leaves and returns is seen to arrive again", () => {
  let record = observePlayers(undefined, ["Notch"], at(0))
  record = observePlayers(record, [], at(20))
  expect(record.players).toEqual({})
  record = observePlayers(record, ["Notch"], at(40))
  expect(record.players.Notch).toEqual({ at: at(40), arrived: true })
})

test("a gap in the answers starts the record again rather than trusting it", () => {
  const first = observePlayers(undefined, ["Notch"], at(0))
  const later = observePlayers(first, ["Notch", "Grumm"], at(600))
  expect(later.players.Notch).toEqual({ at: at(600), arrived: false })
  expect(later.players.Grumm).toEqual({ at: at(600), arrived: false })
})

test("an answer older than the record adds nothing to it", () => {
  const record = observePlayers(observePlayers(undefined, ["Notch"], at(0)), ["Notch"], at(20))
  expect(observePlayers(record, [], at(10))).toBe(record)
})

test("the same answer twice is the same record", () => {
  const record = observePlayers(undefined, ["Notch"], at(0))
  expect(sameRecord(record, observePlayers(record, ["Notch"], at(0)))).toBe(true)
  expect(sameRecord(record, observePlayers(record, ["Notch"], at(20)))).toBe(false)
  expect(sameRecord(undefined, record)).toBe(false)
  expect(sameRecord(record, observePlayers(record, ["Notch", "jeb_"], at(0)))).toBe(false)
})

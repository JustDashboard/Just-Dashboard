import { describe, expect, test } from "bun:test"
import {
  ipamCounts,
  readIPAMView,
  previewReading,
  reservationLink,
  reservationState,
  selectedReservation,
} from "./network-ipam"
describe("shared planning evidence", () => {
  test("missing inventory is a failed read rather than an empty healthy plan", () => {
    for (const value of [null, [], {}, { reservations: [] }])
      expect(() => readIPAMView(value)).toThrow("incomplete response")
    const empty = {
      pools: [],
      reservations: [],
      utilization: [],
      limitations: [],
      inventory: { observations: [], coverage: [] },
    }
    expect(readIPAMView(empty)).toBe(empty)
    expect(() => readIPAMView({ ...empty, inventory: { observations: [] } })).toThrow(
      "incomplete response",
    )
  })
  test("unknown coverage and no known overlap never claim free native/provider space", () => {
    expect(previewReading({ status: "unknown_coverage" }).label).toBe("Coverage incomplete")
    expect(previewReading({ status: "no_known_overlap" }).detail).toContain("does not prove")
    expect(previewReading({ status: "known_overlap" }).label).toBe("Known overlap")
  })
  test("failed/active owner responses keep their planning allocation held", () => {
    expect(reservationState.review_required).toContain("allocation held")
    expect(reservationState.handing_off).toContain("allocation held")
    expect(
      ipamCounts({
        pools: [],
        reservations: [{ state: "review_required" }, { state: "observed" }, { state: "released" }],
        inventory: { observations: [], coverage: [] },
      }).reservations,
    ).toBe(2)
  })
  test("typed handoffs select only exact reserved owner identities", () => {
    const rows = [
      { id: "one", owner: "docker_network", state: "reserved" },
      { id: "two", owner: "wireguard_server", state: "review_required" },
    ]
    expect(selectedReservation(rows, "one", "wireguard_server")).toBeUndefined()
    expect(selectedReservation(rows, "two", "wireguard_server")).toBeUndefined()
    expect(selectedReservation(rows, "one", "docker_network")).toBe(rows[0])
    expect(reservationLink(rows[0])).toBe("/docker/networks?ipamReservation=one")
    expect(reservationLink(rows[1])).toBeUndefined()
  })
})

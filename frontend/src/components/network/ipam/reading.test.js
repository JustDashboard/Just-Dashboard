import { expect, test } from "bun:test"
import { exact, overlaps, ownerProduct, parsePrefix, poolBand, poolOwners } from "./reading"

test("a prefix is its first and last address, in either family", () => {
  expect(parsePrefix("10.240.7.0/24")).toEqual({
    v6: false,
    start: BigInt(0x0af00700),
    end: BigInt(0x0af007ff),
  })
  const v6 = parsePrefix("fd48:abcd::/48")
  expect(v6.v6).toBe(true)
  expect(v6.end - v6.start).toBe((BigInt(1) << BigInt(80)) - BigInt(1))
  expect(parsePrefix("::/0").start).toBe(BigInt(0))
})

test("what is not a prefix is left out rather than guessed at", () => {
  for (const bad of [
    "",
    "10.0.0.0",
    "10.0.0/24",
    "300.0.0.0/8",
    "10.0.0.0/33",
    "fd48::1::/64",
    "x/8",
  ])
    expect(parsePrefix(bad)).toBeUndefined()
})

test("overlap is within one family", () => {
  expect(overlaps("10.240.0.0/16", "10.240.9.0/24")).toBe(true)
  expect(overlaps("10.240.9.0/24", "10.240.0.0/16")).toBe(true)
  expect(overlaps("10.240.0.0/16", "10.241.0.0/24")).toBe(false)
  expect(overlaps("fd48:abcd::/48", "fd48:abcd:0:7::/64")).toBe(true)
  expect(overlaps("fd48:abcd::/48", "fd48:abce::/64")).toBe(false)
  expect(overlaps("::/0", "10.0.0.0/8")).toBe(false)
})

test("an owner word names its product", () => {
  expect(ownerProduct("docker")).toBe("docker")
  expect(ownerProduct("wireguard_peer")).toBe("wireguard")
  expect(ownerProduct("tailscale_approved")).toBe("tailscale")
  expect(ownerProduct("route")).toBeUndefined()
})

const pool = { id: "p", name: "Pool", prefix: "10.240.0.0/16", family: "inet", allocationBits: 24 }
const view = {
  pools: [pool],
  reservations: [
    { poolId: "p", owner: "wireguard_server", state: "reserved" },
    { poolId: "p", owner: "interface_address", state: "observed" },
    { poolId: "p", owner: "docker_network", state: "released" },
    { poolId: "other", owner: "provider_range", state: "reserved" },
  ],
  inventory: {
    observations: [
      { prefix: "10.240.1.0/24", owner: "tailscale_offer" },
      { prefix: "10.240.2.0/24", owner: "route" },
      { prefix: "192.168.0.0/24", owner: "docker" },
      { prefix: "not a prefix", owner: "docker" },
    ],
  },
}

test("a pool's owners are its held plans and what was seen inside it, products first", () => {
  expect(poolOwners(pool, view)).toEqual(["wireguard", "tailscale", "interface", "route"])
})

test("the band counts a block both planned and observed once", () => {
  const band = poolBand({
    totalBlocks: "256",
    reservedBlocks: "2",
    unavailableBlocks: "4",
    candidateBlocks: "252",
  })
  expect(band.observedOnly).toBe(BigInt(2))
  expect(band.share(band.planned)).toBeCloseTo(2 / 256)
  expect(band.share(band.free)).toBeCloseTo(252 / 256)
})

test("shares hold for counts past what a float can count", () => {
  const band = poolBand({
    totalBlocks: "18446744073709551616",
    reservedBlocks: "1",
    unavailableBlocks: "1",
    candidateBlocks: "18446744073709551615",
  })
  expect(band.share(band.free)).toBeCloseTo(1)
  expect(band.share(band.planned)).toBe(0)
  expect(exact("18446744073709551615")).toBe("18,446,744,073,709,551,615")
})

test("a pool with no blocks has no shares", () => {
  const band = poolBand({
    totalBlocks: "0",
    reservedBlocks: "0",
    unavailableBlocks: "0",
    candidateBlocks: "0",
  })
  expect(band.share(band.free)).toBe(0)
})

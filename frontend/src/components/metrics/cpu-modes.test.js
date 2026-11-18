import { expect, test } from "bun:test"
import { cpuModes } from "./cpu-modes"

test("says where the processor's time went, in whole percents", () => {
  expect(cpuModes({ user: 12.4, system: 3.6, iowait: 0.2 })).toBe("12% user · 4% sys · 0% wait")
})

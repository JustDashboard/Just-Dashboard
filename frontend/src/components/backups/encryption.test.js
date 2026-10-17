import { describe, expect, test } from "bun:test"
import { encrypts, keyReach } from "./encryption"

describe("encrypts", () => {
  test("an archive leaving the server is sealed until the operator says otherwise", () => {
    expect(encrypts(null, "s3")).toBe(true)
    expect(encrypts(null, "b2")).toBe(true)
    expect(encrypts(false, "s3")).toBe(false)
  })

  test("one kept on this server's disk is not, unless asked", () => {
    expect(encrypts(null, "local")).toBe(false)
    expect(encrypts(true, "local")).toBe(true)
  })
})

describe("keyReach", () => {
  const key = { exists: true, encryptedArchives: 0, encryptingJobs: 0 }

  test("names the archives it opens and the jobs sealing to it", () => {
    expect(keyReach({ ...key, encryptedArchives: 14, encryptingJobs: 2 })).toBe(
      "14 archives · 2 jobs encrypting",
    )
    expect(keyReach({ ...key, encryptedArchives: 1, encryptingJobs: 1 })).toBe(
      "1 archive · 1 job encrypting",
    )
  })

  test("says nothing it has no figure for", () => {
    expect(keyReach({ ...key, encryptingJobs: 1 })).toBe("1 job encrypting")
    expect(keyReach({ ...key, encryptedArchives: 3 })).toBe("3 archives")
    expect(keyReach(key)).toBe("")
  })
})

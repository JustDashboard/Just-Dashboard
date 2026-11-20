import { describe, expect, test } from "bun:test"
import { aboutTime, transferWords } from "./transfer-words"

const GB = 1024 ** 3

describe("how far a transfer is, in words", () => {
  test("an upload says what is in and roughly how long is left", () => {
    expect(
      transferWords(
        { kind: "upload", status: "uploading", size: 4 * GB, sent: GB, rate: 10 * 1024 ** 2 },
        0,
      ),
    ).toBe("1.0 GB of 4.0 GB, about 5 minutes left")
    expect(transferWords({ kind: "upload", status: "uploading", size: 100, sent: 10 }, 0)).toBe(
      "10 B of 100 B",
    )
  })

  test("a dropped connection says where it stopped and when it tries again", () => {
    expect(
      transferWords(
        { kind: "upload", status: "reconnecting", size: 4 * GB, sent: GB, retryAt: 8_000 },
        0,
      ),
    ).toBe("Connection lost at 1.0 GB; trying again in a few seconds")
  })

  test("paused, stopped and a download of unknown size", () => {
    expect(transferWords({ kind: "upload", status: "paused", size: 2 * GB, sent: GB }, 0)).toBe(
      "Paused at 1.0 GB of 2.0 GB",
    )
    expect(transferWords({ kind: "upload", status: "stopped", size: 2 * GB, sent: GB }, 0)).toBe(
      "Stopped at 1.0 GB of 2.0 GB; add it again to carry on",
    )
    expect(transferWords({ kind: "fetch", status: "uploading", size: -1, sent: 2048 }, 0)).toBe(
      "2.0 KB so far",
    )
    expect(transferWords({ kind: "fetch", status: "uploading", size: -1, sent: 0 }, 0)).toBe(
      "Connecting",
    )
  })

  test("a failure is its own sentence", () => {
    expect(
      transferWords(
        { kind: "fetch", status: "failed", size: -1, sent: 0, error: "Not enough room." },
        0,
      ),
    ).toBe("Not enough room.")
  })

  test("times read as a person says them", () => {
    expect(aboutTime(4)).toBe("a few seconds")
    expect(aboutTime(42)).toBe("40 seconds")
    expect(aboutTime(70)).toBe("about a minute")
    expect(aboutTime(60 * 60 + 20 * 60)).toBe("about 1 hour 20 minutes")
    expect(aboutTime(3 * 3600 + 120)).toBe("about 3 hours")
  })
})

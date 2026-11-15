import { describe, expect, test } from "bun:test"
import { reachability } from "@/lib/remote-desktop"
import {
  agoWord,
  pathWord,
  readingLine,
  signInAddress,
  signInGap,
  stateWord,
} from "./computer-reading"

const NOW = Date.UTC(2026, 10, 14, 12, 0, 0)
const at = (secondsAgo) => NOW / 1000 - secondsAgo

const computer = (extra = {}) => ({
  id: "0a",
  name: "office-pc",
  os: "windows",
  host: "office-pc",
  protocol: "rdp",
  port: 3389,
  username: "wayy",
  hasPassword: true,
  live: 0,
  lastSessionAt: at(11 * 60 + 36),
  peer: { direct: true, relay: "" },
  reach: { state: "open", latencyMs: 43.4, checkedAt: 0 },
  ...extra,
})

describe("a computer's row in words", () => {
  test("the round trip, the path and the last use are one line", () => {
    const pc = computer()
    expect(readingLine(pc, reachability(pc), NOW)).toBe("43 ms direct · used 11m ago")
  })

  test("a relayed path names its relay, and a computer off the tailnet is on the network", () => {
    expect(pathWord(computer({ peer: { direct: false, relay: "fra" } }))).toBe("relay fra")
    expect(pathWord(computer({ peer: { direct: false, relay: "" } }))).toBe("tailnet")
    expect(pathWord(computer({ peer: undefined }))).toBe("network")
  })

  test("a computer that is off or not answering has no round trip to say", () => {
    const off = computer({ reach: { state: "offline", checkedAt: 0 }, lastSessionAt: 0 })
    expect(readingLine(off, reachability(off), NOW)).toBe("never used")
    const closed = computer({
      reach: { state: "closed", error: "refused", checkedAt: 0 },
      lastSessionAt: at(4 * 86_400),
    })
    expect(readingLine(closed, reachability(closed), NOW)).toBe("used 4d ago")
  })

  test("the time is said to its largest unit, so it does not tick at every poll", () => {
    expect(agoWord(at(20), NOW)).toBe("just now")
    expect(agoWord(at(59 * 60 + 59), NOW)).toBe("59m ago")
    expect(agoWord(at(3 * 3600 + 120), NOW)).toBe("3h ago")
    expect(agoWord(at(23 * 3600), NOW)).toBe("23h ago")
    expect(agoWord(at(24 * 3600), NOW)).toBe("1d ago")
    expect(agoWord(at(49 * 3600), NOW)).toBe("2d ago")
  })

  test("the state is a word or two, with the protocol left to the line beside it", () => {
    expect(stateWord(reachability(computer()))).toBe("Ready")
    expect(stateWord(reachability(computer({ live: 1 })))).toBe("In a session")
    expect(
      stateWord(reachability(computer({ reach: { state: "closed", error: "timeout" } }))),
    ).toBe("Not answering")
    expect(stateWord(reachability(computer({ reach: { state: "offline" } })))).toBe("Offline")
  })

  test("the sign-in is written as a shell writes it, and what is not saved is said", () => {
    expect(signInAddress(computer())).toBe("wayy@office-pc:3389")
    expect(signInAddress(computer({ username: "", port: 5900 }))).toBe("office-pc:5900")
    expect(signInGap(computer())).toBeUndefined()
    expect(signInGap(computer({ username: "", hasPassword: false }))).toBe(
      "no account saved, no password saved",
    )
    // Screen Sharing and VNC sign in with a password alone.
    expect(signInGap(computer({ username: "", protocol: "vnc" }))).toBeUndefined()
  })
})

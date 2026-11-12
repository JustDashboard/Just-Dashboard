import { describe, expect, test } from "bun:test"
import { installSentence, offerTagNote, pendingNote } from "./update-offer"

const check = { ref: "main" }

describe("what the Version page says about the release on offer", () => {
  test("a tagged release is named by its tag", () => {
    const report = { version: "0.8.5", latest: "0.8.6", available: true, tagged: true, check }
    expect(offerTagNote(report)).toBe("tagged v0.8.6")
    expect(installSentence(report)).toBe("Installs 0.8.6 over 0.8.5 and restarts the dashboard.")
  })

  test("a fork's untagged branch head is said to be one, before it is installed", () => {
    const report = { version: "0.8.5", latest: "0.8.6", available: true, tagged: false, check }
    expect(offerTagNote(report)).toBe("no release tag, installs main as it stands")
    expect(installSentence(report)).toContain("from the head of main, which has no release tag")
  })

  test("a report from a server that predates the field is not called a release", () => {
    expect(offerTagNote({ latest: "0.8.6", available: true, check })).toContain("no release tag")
  })

  test("nothing on offer says nothing about tags", () => {
    expect(offerTagNote({ latest: "0.8.5", available: false, tagged: false, check })).toBe("")
  })

  test("a version on the branch with no tag is named as not released", () => {
    expect(pendingNote({ available: false, pending: "0.8.6", check })).toBe(
      "0.8.6 is on main, not released yet",
    )
    expect(pendingNote({ available: false, check })).toBe("")
  })
})

import { expect, test } from "bun:test"
import { folderHref, matchName, visitFolder } from "./navigation"

test("folder navigation retains unrelated URL context and clears the old entry", () => {
  expect(
    folderHref("/srv/a b/", "https://dashboard.test/files?entry=old&source=storage#listing"),
  ).toBe("/files?source=storage&path=%2Fsrv%2Fa+b#listing")
})

test("a new visit after Back discards only the forward branch", () => {
  const history = { id: "tab", paths: ["/home", "/home/photos", "/etc"], index: 1 }
  expect(visitFolder(history, "/srv")).toEqual({
    id: "tab",
    paths: ["/home", "/home/photos", "/srv"],
    index: 2,
  })
  expect(visitFolder(history, "/home/photos/")).toBe(history)
  expect(history.paths).toEqual(["/home", "/home/photos", "/etc"])
})

test("name typing wraps, narrows prefixes and tolerates no match", () => {
  const names = ["photos", "site", "server.log", "notes.md"]
  expect(matchName(names, "s", -1)).toBe(1)
  expect(matchName(names, "s", 1)).toBe(2)
  expect(matchName(names, "s", 2)).toBe(1)
  expect(matchName(names, "se", 1)).toBe(2)
  expect(matchName(names, "missing", 2)).toBe(-1)
  expect(matchName([], "s", -1)).toBe(-1)
})

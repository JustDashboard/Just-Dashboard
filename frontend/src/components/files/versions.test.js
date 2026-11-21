import { expect, test } from "bun:test"
import {
  editorKey,
  editorSearch,
  historyHref,
  historySearch,
  pickedVersion,
  previousVersion,
  sameEditorFile,
  versionLine,
  versionName,
  versionsSentence,
} from "./versions"

const edit = (id, extra = {}) => ({
  id,
  kind: "edit",
  at: "2026-11-20T10:00:00Z",
  by: "admin",
  ...extra,
})

test("the history is a view in the address, and the editor is keyed without it", () => {
  const open = historySearch("path=%2Fetc%2Fhosts&root=%2Fetc", "e4", "previous")
  expect(open).toBe(
    "/files/editor?path=%2Fetc%2Fhosts&root=%2Fetc&view=history&version=e4&against=previous",
  )
  expect(editorSearch(open.split("?")[1])).toBe("/files/editor?path=%2Fetc%2Fhosts&root=%2Fetc")
  expect(editorKey(open.split("?")[1])).toBe(editorKey("path=%2Fetc%2Fhosts&root=%2Fetc"))
  expect(historySearch("path=%2Fa&version=e1&against=previous")).toBe(
    "/files/editor?path=%2Fa&view=history",
  )
  expect(historyHref("/srv/app.conf", "/srv")).toBe(
    "/files/editor?path=%2Fsrv%2Fapp.conf&root=%2Fsrv&view=history",
  )
})

test("moving into and out of the same file's history is not leaving the editor", () => {
  const here = "http://dash.test/files/editor?path=%2Fetc%2Fhosts"
  expect(sameEditorFile("/files/editor?path=%2Fetc%2Fhosts&view=history&version=e2", here)).toBe(
    true,
  )
  expect(sameEditorFile("/files/editor?path=%2Fetc%2Fhostname&view=history", here)).toBe(false)
  expect(sameEditorFile("/files?path=%2Fetc", here)).toBe(false)
  expect(sameEditorFile("https://elsewhere.test/files/editor?path=%2Fetc%2Fhosts", here)).toBe(
    false,
  )
})

test("a pick that is gone falls back to the newest, and previous is the next older row", () => {
  const history = { versions: [edit("e3"), edit("e2"), { id: "b9", kind: "backup", at: "x" }] }
  expect(pickedVersion(history, "e2").id).toBe("e2")
  expect(pickedVersion(history, "e99").id).toBe("e3")
  expect(pickedVersion({ versions: [] }, "e1")).toBeUndefined()
  expect(previousVersion(history, history.versions[1]).id).toBe("b9")
  expect(previousVersion(history, history.versions[2])).toBeUndefined()
})

test("each kind of version says what it is in a few words", () => {
  expect(versionName(edit("e1", { reason: "save" }))).toBe("Before a save by admin")
  expect(versionName(edit("e1", { reason: "restore" }))).toBe("Before a restore by admin")
  expect(versionName(edit("e1", { reason: "image" }))).toBe("Before a picture save by admin")
  expect(versionName(edit("e1", { reason: "save", outside: true }))).toBe(
    "Changed outside the dashboard",
  )
  expect(versionName(edit("e1", { reason: "displaced" }))).toBe("Saved by admin")
  const backup = {
    id: "b1",
    kind: "backup",
    at: "x",
    backup: { job: "nightly", targetKind: "b2", local: false },
  }
  expect(versionName(backup)).toBe("nightly")
  expect(versionLine(backup, "3 days ago")).toBe("Backup 3 days ago · in Backblaze B2")
  expect(versionLine({ ...backup, backup: { ...backup.backup, local: true } }, "1 day ago")).toBe(
    "Backup 1 day ago · kept on this server",
  )
  expect(versionLine(edit("e1", { outside: true }), "2 hours ago")).toBe(
    "Found 2 hours ago by admin's save",
  )
  expect(versionLine(edit("e1", { reason: "displaced" }), "3 days ago")).toBe(
    "3 days ago · then changed outside",
  )
})

test("the details line counts versions and says when one cannot be kept", () => {
  const ago = () => "an hour ago"
  const base = { keeping: true, tooLarge: false, versions: [] }
  expect(versionsSentence(base, ago)).toBe("No earlier versions yet")
  expect(versionsSentence({ ...base, tooLarge: true }, ago)).toBe(
    "Too large to keep earlier versions",
  )
  expect(versionsSentence({ ...base, keeping: false }, ago)).toBe("Earlier versions are not kept")
  expect(versionsSentence({ ...base, versions: [edit("e1")] }, ago)).toBe(
    "1, last changed an hour ago",
  )
  expect(versionsSentence({ ...base, versions: [edit("e1"), edit("e2")] }, ago)).toBe(
    "2, last changed an hour ago",
  )
})

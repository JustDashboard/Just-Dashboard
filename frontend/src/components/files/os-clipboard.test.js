import { describe, expect, test } from "bun:test"
import { CLIP_TYPE, pastedFileName, pastedTextName, readPaste, writeClip } from "./os-clipboard"

function transfer(data = {}, files = 0) {
  const store = new Map(Object.entries(data))
  return {
    get types() {
      return [...store.keys(), ...(files > 0 ? ["Files"] : [])]
    },
    files: { length: files },
    getData: (type) => store.get(type) ?? "",
    setData: (type, value) => store.set(type, value),
  }
}

describe("system clipboard", () => {
  const held = { mode: "copy", paths: ["/srv/a.txt", "/srv/b"] }

  test("a copy writes the paths as text and in our own format", () => {
    const data = transfer()
    writeClip(data, held)
    expect(data.getData("text/plain")).toBe("/srv/a.txt\n/srv/b")
    expect(readPaste(data, null)).toEqual({ kind: "paths", clip: held })
  })

  test("files copied on the desktop are uploaded even while paths are held", () => {
    expect(readPaste(transfer({}, 2), held)).toEqual({ kind: "files" })
  })

  test("text that is not the held paths becomes a file", () => {
    expect(readPaste(transfer({ "text/plain": "hello" }), held)).toEqual({
      kind: "text",
      text: "hello",
    })
  })

  test("the held paths still paste when a browser drops our format", () => {
    const data = transfer({ "text/plain": "/srv/a.txt\n/srv/b" })
    expect(readPaste(data, held)).toEqual({ kind: "paths", clip: held })
    expect(readPaste(transfer(), held)).toEqual({ kind: "paths", clip: held })
    expect(readPaste(transfer(), null)).toBeNull()
  })

  test("file locations without the files are not pasted as text", () => {
    expect(readPaste(transfer({ "text/uri-list": "file:///home/me/a.png\r\n" }), null)).toEqual({
      kind: "locations",
    })
    expect(readPaste(transfer({ "text/plain": "file:///home/me/a.png" }), null)).toEqual({
      kind: "locations",
    })
    expect(readPaste(transfer({ "text/plain": "see file:///x" }), null)).toEqual({
      kind: "text",
      text: "see file:///x",
    })
  })

  test("a malformed clip of ours is not trusted", () => {
    const data = transfer({ [CLIP_TYPE]: '{"mode":"move","paths":["x"]}', "text/plain": "x" })
    expect(readPaste(data, null)).toEqual({ kind: "text", text: "x" })
  })

  test("pasted screenshots and text are named by when they arrived", () => {
    const at = new Date(2026, 9, 24, 9, 5, 7)
    expect(pastedFileName("image.png", "image/png", at)).toBe(
      "Pasted image 2026-10-24 09.05.07.png",
    )
    expect(pastedFileName("report.pdf", "application/pdf", at)).toBe("report.pdf")
    expect(pastedFileName("image.png", "", at)).toBe("image.png")
    expect(pastedTextName(at)).toBe("Pasted text 2026-10-24 09.05.07.txt")
  })
})

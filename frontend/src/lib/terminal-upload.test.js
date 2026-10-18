import { describe, expect, test } from "bun:test"
import {
  chooseClipboardImage,
  insertTerminalPath,
  interceptClipboardImagePaste,
  uploadTerminalImage,
} from "./terminal-upload"

function item(kind, type, file = null) {
  return { kind, type, getAsFile: () => file }
}

describe("terminal clipboard images", () => {
  test("normal text paste is left completely unchanged", () => {
    let prevented = false
    let uploaded = false
    const handled = interceptClipboardImagePaste(
      {
        clipboardData: { items: [item("string", "text/plain")] },
        preventDefault: () => {
          prevented = true
        },
      },
      () => {
        uploaded = true
      },
      () => {},
    )

    expect(handled).toBe(false)
    expect(prevented).toBe(false)
    expect(uploaded).toBe(false)
  })

  test.each(["image/png", "image/jpeg", "image/webp"])("accepts %s clipboard data", (mime) => {
    const file = new File(["image"], `screenshot.${mime.split("/")[1]}`, { type: mime })
    expect(chooseClipboardImage([item("file", mime, file)])).toEqual({ kind: "image", file })
  })

  test("intercepts an unsupported clipboard image without treating it as text", () => {
    let prevented = false
    let rejected = ""
    const handled = interceptClipboardImagePaste(
      {
        clipboardData: { items: [item("file", "image/gif")] },
        preventDefault: () => {
          prevented = true
        },
      },
      () => {},
      (mime) => {
        rejected = mime
      },
    )

    expect(handled).toBe(true)
    expect(prevented).toBe(true)
    expect(rejected).toBe("image/gif")
  })

  test("inserts the uploaded path through terminal input without Enter", () => {
    const sent = []
    const path = `/tmp/just-dashboard/abc123abc123abcd/clipboard-${"f".repeat(32)}.png`
    insertTerminalPath(path, (data) => sent.push(data))
    expect(sent).toEqual([path])
  })

  test("refuses a returned path containing terminal control characters", () => {
    expect(() => insertTerminalPath("/tmp/screenshot.png\nrm -rf /", () => {})).toThrow()
    expect(() =>
      insertTerminalPath("/tmp/just-dashboard/a;rm-rf/clipboard-image.png", () => {}),
    ).toThrow()
  })

  test("sends CSRF proof with the multipart upload", async () => {
    const originalFetch = globalThis.fetch
    let request
    globalThis.fetch = async (input, init) => {
      request = { input, init }
      return new Response(
        JSON.stringify({
          path: `/tmp/just-dashboard/abc123abc123abcd/clipboard-${"f".repeat(32)}.png`,
          name: "screenshot.png",
          mime: "image/png",
          size: 5,
        }),
        { status: 201 },
      )
    }

    try {
      await uploadTerminalImage(
        "abc123abc123abcd",
        new File(["image"], "screenshot.png", { type: "image/png" }),
      )
    } finally {
      globalThis.fetch = originalFetch
    }

    expect(request.input).toBe("/api/v1/terminal/abc123abc123abcd/clipboard")
    expect(request.init.method).toBe("POST")
    expect(Object.fromEntries(new Headers(request.init.headers))).toEqual({ "x-jd-csrf": "1" })
  })
})

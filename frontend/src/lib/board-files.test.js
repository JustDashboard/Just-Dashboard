import { expect, test } from "bun:test"
import { dataURLBytes, filesToFetch, filesToUpload, shownFileIds } from "./board-files"

const image = (fileId, patch = {}) => ({ type: "image", fileId, ...patch })

test("only the images still on the drawing count", () => {
  const ids = shownFileIds([
    image("a"),
    image("b", { isDeleted: true }),
    image(null),
    { type: "rectangle", fileId: "c" },
  ])
  expect([...ids]).toEqual(["a"])
})

test("a browser reads the shown files it does not hold", () => {
  expect(filesToFetch([image("a"), image("b")], { a: {} })).toEqual(["b"])
})

test("a file is sent once, and never one of a type the server refuses", () => {
  const held = {
    a: { mimeType: "image/png", dataURL: "data:image/png;base64,AA==" },
    b: { mimeType: "image/png", dataURL: "data:image/png;base64,AA==" },
    c: { mimeType: "application/pdf", dataURL: "data:application/pdf;base64,AA==" },
  }
  expect(
    filesToUpload([image("a"), image("b"), image("c"), image("d")], held, new Set(["a"])),
  ).toEqual(["b"])
})

test("a data URL decodes without fetch, base64 or not", () => {
  expect([...dataURLBytes("data:image/png;base64,AAEC/w==")]).toEqual([0, 1, 2, 255])
  expect(new TextDecoder().decode(dataURLBytes("data:image/svg+xml,%3Csvg%2F%3E"))).toBe("<svg/>")
})

import { describe, expect, test } from "bun:test"
import { MAX_IMAGES, fitWithin, imageFiles, imageSource, takeImages } from "./images"
import { beginTurn, EMPTY_AGENT, replayLines } from "./model"

describe("pasted images", () => {
  test("a screenshot is held to what a model reads at full detail, in proportion", () => {
    expect(fitWithin(2880, 1800, 1568)).toEqual({ width: 1568, height: 980 })
    expect(fitWithin(1200, 3000, 1568)).toEqual({ width: 627, height: 1568 })
    expect(fitWithin(800, 600, 1568)).toEqual({ width: 800, height: 600 })
  })
  test("only images are taken from what was pasted or dropped", () => {
    const png = new File(["x"], "shot.png", { type: "image/png" })
    const text = new File(["x"], "notes.txt", { type: "text/plain" })
    expect(imageFiles([png, text, null])).toEqual([png])
  })
  test("a question carries at most four, and says how many were left", () => {
    expect(MAX_IMAGES).toBe(4)
    expect(takeImages(3, ["a", "b", "c"])).toEqual({ taken: ["a"], left: 2 })
    expect(takeImages(4, ["a"])).toEqual({ taken: [], left: 1 })
    expect(takeImages(0, ["a", "b"])).toEqual({ taken: ["a", "b"], left: 0 })
  })
  test("a question shows its images, and a saved chat draws them again", () => {
    const pasted = { thumb: "data:image/jpeg;base64,AAA", full: "data:image/png;base64,BBB" }
    const asked = beginTurn(EMPTY_AGENT, "t1", "", [pasted])
    expect(asked.turns[0].images).toEqual([pasted])
    expect(beginTurn(EMPTY_AGENT, "t1", "plain").turns[0].images).toBeUndefined()
    const thumb = { mediaType: "image/jpeg", data: "QUJD" }
    const replayed = replayLines([
      { type: "question", text: "What is this?", images: [thumb, { mediaType: 1 }] },
      { type: "text", text: "A 502." },
    ])
    expect(replayed.turns[0].images).toEqual([{ thumb: imageSource(thumb) }])
    expect(replayed.turns[0].question).toBe("What is this?")
  })
  test("a saved chat's image opens whole from where the server keeps it, at its size", () => {
    const kept = { mediaType: "image/jpeg", data: "QUJD", id: "img 1", width: 1568, height: 980 }
    const lines = [{ type: "question", text: "", images: [kept, { ...kept, id: "", width: "x" }] }]
    const replayed = replayLines(lines, { imageAt: (id) => `/chat/c1/images/${id}` })
    expect(replayed.turns[0].images).toEqual([
      { thumb: imageSource(kept), full: "/chat/c1/images/img 1", width: 1568, height: 980 },
      { thumb: imageSource(kept) },
    ])
    // Read without a place to read them, only the thumbnails are drawn.
    expect(replayLines(lines).turns[0].images[0]).toEqual({
      thumb: imageSource(kept),
      width: 1568,
      height: 980,
    })
  })
})

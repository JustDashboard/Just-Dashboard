import { describe, expect, test } from "bun:test"
import {
  aheadOf,
  clockTime,
  hasSlideshow,
  nextPicture,
  positionWords,
  queueFrom,
  stripWindow,
  swipeStep,
} from "./gallery"

const file = (name, dir = "/music") => ({
  name,
  path: `${dir}/${name}`,
  modified: "2026-11-20T00:00:00Z",
  isDir: false,
})

describe("the audio queue", () => {
  const folder = [
    { ...file("covers"), isDir: true },
    file("01 intro.mp3"),
    file("cover.jpg"),
    file("02 song.flac"),
    file("03 outro.ogg"),
  ]

  test("playing a track queues its folder's audio in order, from that track", () => {
    const { tracks, index } = queueFrom(folder[3], folder)
    expect(tracks.map((t) => t.name)).toEqual(["01 intro.mp3", "02 song.flac", "03 outro.ogg"])
    expect(index).toBe(1)
    expect(tracks[0].folder).toBe("/music")
  })

  test("a track that is not in the listing is a queue of one", () => {
    const { tracks, index } = queueFrom(file("lone.mp3", "/elsewhere"), folder)
    expect(tracks).toHaveLength(1)
    expect(tracks[0].folder).toBe("/elsewhere")
    expect(index).toBe(0)
  })

  test("times read as a clock does", () => {
    expect(clockTime(7.9)).toBe("0:07")
    expect(clockTime(225)).toBe("3:45")
    expect(clockTime(3723)).toBe("1:02:03")
    expect(clockTime(NaN)).toBe("0:00")
  })
})

describe("the gallery", () => {
  const pictures = ["a.jpg", "b.png", "c.gif"].map((n) => file(n, "/photos"))
  const mixed = [file("a.jpg"), file("notes.txt"), file("b.jpg"), file("clip.mp4"), file("c.jpg")]

  test("how far through, in words", () => {
    expect(positionWords(1, pictures)).toBe("Picture 2 of 3")
    expect(positionWords(1, mixed)).toBe("2 of 5")
    expect(positionWords(0, [pictures[0]])).toBeNull()
  })

  test("the strip is a window round the current item", () => {
    expect(stripWindow(10, 3)).toEqual([0, 10])
    expect(stripWindow(1000, 500, 40)).toEqual([460, 541])
    expect(stripWindow(1000, 2, 40)).toEqual([0, 81])
    expect(stripWindow(1000, 999, 40)).toEqual([919, 1000])
  })

  test("the next pictures are fetched ahead; a text file is not", () => {
    expect(aheadOf(mixed, 0)).toEqual([2])
    expect(aheadOf(mixed, 2)).toEqual([4])
    expect(aheadOf(pictures, 1)).toEqual([2, 0])
  })

  test("the slideshow steps over what is not a picture and stops at the end", () => {
    expect(nextPicture(mixed, 0)).toBe(2)
    expect(nextPicture(mixed, 2)).toBe(4)
    expect(nextPicture(mixed, 4)).toBeNull()
    expect(hasSlideshow(mixed)).toBe(true)
    expect(hasSlideshow([file("a.jpg"), file("x.txt")])).toBe(false)
  })

  test("a sideways swipe steps; a scroll or a small move does not", () => {
    expect(swipeStep(-80, 10)).toBe(1)
    expect(swipeStep(80, 10)).toBe(-1)
    expect(swipeStep(-30, 0)).toBe(0)
    expect(swipeStep(-60, 70)).toBe(0)
  })
})

/**
 * Images pasted into the assistant's box: a screenshot of an error, a page that
 * looks wrong. Each is made small here before it is sent. Its longest side is held
 * to what the models read at full detail (1568 pixels; past that they shrink it
 * themselves and the bytes are wasted), and it is encoded as a PNG, or as a JPEG
 * when the PNG is large. A thumbnail goes with it, which the saved chat keeps to
 * draw the question again. The server checks the same bounds (`assistant/image.go`).
 */

export const MAX_IMAGES = 4
export const IMAGE_EDGE = 1568
export const THUMB_EDGE = 320
/** The server's bound on one image, decoded; the page aims well under it. */
export const MAX_IMAGE_BYTES = 1536 * 1024
const TARGET_BYTES = 1024 * 1024
const MAX_THUMB_BYTES = 96 * 1024

export interface EncodedImage {
  mediaType: string
  /** The bytes, in base64. */
  data: string
}

/** An image as the box holds it, ready to send. */
export interface PastedImage extends EncodedImage {
  id: string
  thumb: EncodedImage
  /** The thumbnail as an address an `<img>` draws. */
  src: string
}

/** A width and height held to `edge` on the longest side, in proportion. */
export function fitWithin(
  width: number,
  height: number,
  edge: number,
): { width: number; height: number } {
  const longest = Math.max(width, height)
  if (longest <= edge || longest <= 0) return { width, height }
  const scale = edge / longest
  return {
    width: Math.max(1, Math.round(width * scale)),
    height: Math.max(1, Math.round(height * scale)),
  }
}

/** The images among what was pasted or dropped. */
export function imageFiles(files: Iterable<File | null>): File[] {
  return [...files].filter((file): file is File => !!file && file.type.startsWith("image/"))
}

/** An encoded image as an address an `<img>` draws. */
export function imageSource(image: EncodedImage): string {
  return `data:${image.mediaType};base64,${image.data}`
}

/** How many more images a question may carry, and what was left out past that. */
export function takeImages<T>(held: number, offered: T[]): { taken: T[]; left: number } {
  const room = Math.max(0, MAX_IMAGES - held)
  return { taken: offered.slice(0, room), left: Math.max(0, offered.length - room) }
}

function draw(
  bitmap: ImageBitmap,
  size: { width: number; height: number },
  opaque: boolean,
): HTMLCanvasElement {
  const canvas = document.createElement("canvas")
  canvas.width = size.width
  canvas.height = size.height
  const context = canvas.getContext("2d")
  if (!context) throw new Error("This browser cannot read images")
  if (opaque) {
    // A JPEG has no transparency, and a transparent screenshot would turn black.
    context.fillStyle = "#ffffff"
    context.fillRect(0, 0, size.width, size.height)
  }
  context.imageSmoothingQuality = "high"
  context.drawImage(bitmap, 0, 0, size.width, size.height)
  return canvas
}

function encode(canvas: HTMLCanvasElement, type: string, quality?: number): Promise<Blob> {
  return new Promise((resolve, reject) =>
    canvas.toBlob(
      (blob) => (blob ? resolve(blob) : reject(new Error("The image could not be made small"))),
      type,
      quality,
    ),
  )
}

async function base64(blob: Blob): Promise<string> {
  const bytes = new Uint8Array(await blob.arrayBuffer())
  let text = ""
  for (let at = 0; at < bytes.length; at += 0x8000) {
    text += String.fromCharCode(...bytes.subarray(at, at + 0x8000))
  }
  return btoa(text)
}

/** Makes one pasted image small enough to send, with its thumbnail. */
export async function prepareImage(file: Blob): Promise<PastedImage> {
  let bitmap: ImageBitmap
  try {
    bitmap = await createImageBitmap(file)
  } catch {
    throw new Error("That image could not be read. Paste a PNG, JPEG, GIF or WebP.")
  }
  try {
    let edge = IMAGE_EDGE
    let blob: Blob | undefined
    // A screenshot fits at once; a photograph may take a smaller size or two.
    for (let attempt = 0; attempt < 4; attempt++) {
      const size = fitWithin(bitmap.width, bitmap.height, edge)
      blob = await encode(draw(bitmap, size, false), "image/png")
      if (blob.size > TARGET_BYTES)
        blob = await encode(draw(bitmap, size, true), "image/jpeg", 0.85)
      if (blob.size <= TARGET_BYTES) break
      edge = Math.round(edge * 0.75)
    }
    if (!blob || blob.size > MAX_IMAGE_BYTES) throw new Error("That image is too large to send.")
    const thumbBlob = await encode(
      draw(bitmap, fitWithin(bitmap.width, bitmap.height, THUMB_EDGE), true),
      "image/jpeg",
      0.8,
    )
    if (thumbBlob.size > MAX_THUMB_BYTES) throw new Error("That image is too large to send.")
    const thumb = { mediaType: "image/jpeg", data: await base64(thumbBlob) }
    return {
      id: crypto.randomUUID(),
      mediaType: blob.type,
      data: await base64(blob),
      thumb,
      src: imageSource(thumb),
    }
  } finally {
    bitmap.close()
  }
}

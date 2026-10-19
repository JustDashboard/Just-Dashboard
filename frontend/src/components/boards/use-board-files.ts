"use client"

import { useCallback, useMemo, useRef } from "react"
import type { ExcalidrawElement } from "@excalidraw/excalidraw/element/types"
import type {
  BinaryFileData,
  BinaryFiles,
  ExcalidrawImperativeAPI,
} from "@excalidraw/excalidraw/types"
import { fetchResponse } from "@/lib/api"
import { blobDataURL, dataURLBytes, filesToFetch, filesToUpload } from "@/lib/board-files"

/**
 * A drawing's images, kept on the server beside its scene. Each is sent once,
 * as soon as it is placed rather than with the next save, so another editor
 * in the live room can read it by the time the element reaches them.
 */
export function useBoardFiles(boardId: number, stored: readonly string[]) {
  const storedRef = useRef(new Set(stored))
  const uploadsRef = useRef(new Map<string, Promise<void>>())
  const readingRef = useRef(new Set<string>())

  const path = useCallback(
    (id: string) => `/boards/${boardId}/files/${encodeURIComponent(id)}`,
    [boardId],
  )

  /** Sends the shown images the server lacks; resolves to the ids this call sent. */
  const upload = useCallback(
    async (elements: readonly ExcalidrawElement[], files: BinaryFiles) => {
      const ids = filesToUpload(elements, files, storedRef.current)
      await Promise.all(
        ids.map((id) => {
          let task = uploadsRef.current.get(id)
          if (!task) {
            const { mimeType, dataURL } = files[id]
            task = fetchResponse(path(id), {
              method: "PUT",
              headers: { "Content-Type": mimeType },
              body: new Blob([dataURLBytes(dataURL)], { type: mimeType }),
            })
              .then(() => {
                storedRef.current.add(id)
              })
              .finally(() => uploadsRef.current.delete(id))
            uploadsRef.current.set(id, task)
          }
          return task
        }),
      )
      return ids
    },
    [path],
  )

  /**
   * Reads the shown images this editor does not hold. One a peer has not sent
   * yet is simply missed; its `files` message, or the next scene, asks again.
   */
  const read = useCallback(
    async (api: ExcalidrawImperativeAPI, elements: readonly ExcalidrawElement[]) => {
      const ids = filesToFetch(elements, api.getFiles()).filter((id) => !readingRef.current.has(id))
      for (const id of ids) readingRef.current.add(id)
      await Promise.all(
        ids.map(async (id) => {
          try {
            const blob = await (await fetchResponse(path(id))).blob()
            api.addFiles([
              {
                id,
                mimeType: blob.type,
                dataURL: await blobDataURL(blob),
                created: Date.now(),
              } as BinaryFileData,
            ])
            storedRef.current.add(id)
          } catch {
            // Missed for now: see above.
          } finally {
            readingRef.current.delete(id)
          }
        }),
      )
    },
    [path],
  )

  /** The server's own list after a save, which has collected the images no element shows. */
  const settle = useCallback((ids: readonly string[]) => {
    storedRef.current = new Set(ids)
  }, [])

  return useMemo(() => ({ upload, read, settle }), [upload, read, settle])
}

"use client"

import { useCallback, useEffect, useRef } from "react"
import {
  getLibraryItemsHash,
  mergeLibraryItems,
  parseLibraryTokensFromUrl,
} from "@excalidraw/excalidraw"
import type { ExcalidrawImperativeAPI, LibraryItems } from "@excalidraw/excalidraw/types"
import { ApiError, get, post, put } from "@/lib/api"
import type { BoardLibrary } from "@/lib/boards"
import { notify } from "@/lib/toast"

/**
 * The install's Excalidraw library, kept on the server. Excalidraw holds its
 * library in memory alone and empties it when the editor closes, so a shape
 * pack added to one drawing was gone by the next; and its own installer
 * fetches the pack from the browser, which `connect-src` refuses. Packs from
 * libraries.excalidraw.com are fetched by the server instead.
 */
export function useBoardLibrary(api: ExcalidrawImperativeAPI | null, canWrite: boolean) {
  const revisionRef = useRef(0)
  // What the server holds, as Excalidraw hashes a library. Null until it has
  // been read: a library saved before then would overwrite the stored one.
  const storedRef = useRef<number | null>(null)
  const pendingRef = useRef<LibraryItems | null>(null)
  const savingRef = useRef(false)

  const save = useCallback(async () => {
    if (savingRef.current || !api) return
    savingRef.current = true
    try {
      while (pendingRef.current) {
        const items = pendingRef.current
        pendingRef.current = null
        const hash = getLibraryItemsHash(items)
        if (hash === storedRef.current) continue
        try {
          const saved = await put<{ revision: number }>("/boards/library", {
            libraryItems: items,
            revision: revisionRef.current,
          })
          revisionRef.current = saved.revision
          storedRef.current = hash
        } catch (cause) {
          if (!(cause instanceof ApiError && cause.code === "library_conflict")) {
            notify.error("The library was not saved", cause)
            return
          }
          // Another drawing saved the library first. Both sets of items are
          // kept; one removed here and still there comes back, which loses
          // nothing anybody made.
          const latest = await get<BoardLibrary>("/boards/library")
          const theirs = latest.libraryItems as LibraryItems
          revisionRef.current = latest.revision
          storedRef.current = getLibraryItemsHash(theirs)
          const merged = mergeLibraryItems(items, theirs)
          pendingRef.current = merged
          void api.updateLibrary({ libraryItems: merged })
        }
      }
    } catch (cause) {
      notify.error("The library was not saved", cause)
    } finally {
      savingRef.current = false
    }
  }, [api])

  useEffect(() => {
    if (!api) return
    let open = true
    const stored = get<BoardLibrary>("/boards/library").then(
      (library) => {
        revisionRef.current = library.revision
        storedRef.current = getLibraryItemsHash(library.libraryItems as LibraryItems)
        return library.libraryItems as LibraryItems
      },
      (cause) => {
        if (open) notify.error("The library could not be read", cause)
        return []
      },
    )
    void api.updateLibrary({ libraryItems: stored, merge: true })

    const install = ({ libraryUrl, idToken }: { libraryUrl: string; idToken: string | null }) => {
      // The address keeps no trace of the hand-off, or a reload would add it again.
      const here = new URL(window.location.href)
      here.hash = ""
      window.history.replaceState(window.history.state, "", here)
      if (!canWrite) {
        notify.error("Adding a library needs permission to edit drawings")
        return
      }
      const file = post<unknown>("/boards/library/fetch", {
        url: decodeURIComponent(libraryUrl),
      }).then((library) => new Blob([JSON.stringify(library)], { type: "application/json" }))
      api
        .updateLibrary({
          libraryItems: file,
          // The library site hands back the token this editor gave it; any
          // other one means the pack was meant for another tab, so ask.
          prompt: idToken !== api.id,
          merge: true,
          defaultStatus: "published",
          openLibraryMenu: true,
        })
        .catch((cause) => notify.error("The library could not be added", cause))
    }
    const handOff = parseLibraryTokensFromUrl()
    if (handOff) install(handOff)
    const onHash = () => {
      const tokens = parseLibraryTokensFromUrl()
      if (tokens) install(tokens)
    }
    window.addEventListener("hashchange", onHash)
    return () => {
      open = false
      window.removeEventListener("hashchange", onHash)
    }
  }, [api, canWrite])

  return useCallback(
    (items: LibraryItems) => {
      if (!canWrite || storedRef.current === null) return
      pendingRef.current = items
      void save()
    },
    [canWrite, save],
  )
}

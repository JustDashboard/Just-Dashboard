"use client"

import { useEffect, useState } from "react"

import { get } from "@/lib/api"
import type { Release, UpdateHistory } from "@/lib/types"

/** How long after a failed read the history is asked for again. */
const RETRY_MS = 5000

/**
 * One read per version for the whole tab, shared by everything that shows release notes. The
 * history is a quarter of a megabyte that changes only with the build, so it is not part of the
 * update report that is polled; the browser keeps it by its ETag across reloads.
 */
let shared: { version: string; releases: Promise<Release[]> } | undefined

function readHistory(version: string): Promise<Release[]> {
  if (shared?.version !== version) {
    const releases = get<UpdateHistory>("/dashboard/update/history").then(
      (answer) => answer?.history ?? [],
    )
    // A failed read is not kept: the next caller asks again.
    releases.catch(() => {
      if (shared?.releases === releases) shared = undefined
    })
    shared = { version, releases }
  }
  return shared.releases
}

/**
 * The release notes this build was compiled with, newest first, or undefined until they have
 * arrived. `version` is the running build's (the update report's), so a new build is read anew;
 * `wanted` lets a component that only sometimes shows notes not ask until it does.
 */
export function useUpdateHistory(
  version: string | undefined,
  wanted = true,
): Release[] | undefined {
  const [loaded, setLoaded] = useState<{ version: string; releases: Release[] }>()
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    if (!version || !wanted) return
    let live = true
    let retry: ReturnType<typeof setTimeout> | undefined
    readHistory(version).then(
      (releases) => {
        if (live) setLoaded({ version, releases })
      },
      () => {
        if (live) retry = setTimeout(() => setAttempt((n) => n + 1), RETRY_MS)
      },
    )
    return () => {
      live = false
      clearTimeout(retry)
    }
  }, [version, wanted, attempt])

  return loaded && loaded.version === version ? loaded.releases : undefined
}

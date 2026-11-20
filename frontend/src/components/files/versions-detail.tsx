"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { ClockRewind } from "@/components/icons"
import { get, shaped } from "@/lib/api"
import { Detail } from "@/components/detail-list"
import type { FileVersions } from "@/components/files/versions-types"
import { historyHref, versionsSentence, whenAgo } from "@/components/files/versions"

/**
 * The details pane's line about a file's earlier contents: how many there
 * are and when it last changed, or why none are kept, as a link to the
 * history. It holds its place while it reads, so the facts under it stay put.
 */
export function VersionsDetail({ path, modified }: { path: string; modified?: string }) {
  const [history, setHistory] = useState<FileVersions>()
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    get<unknown>("/files/versions", { path }, controller.signal)
      .then((answer) => {
        setHistory(shaped<FileVersions>(answer, "versions", "the file's versions"))
        setFailed(false)
      })
      .catch(() => !controller.signal.aborted && setFailed(true))
    return () => controller.abort()
  }, [path, modified])

  return (
    <Detail icon={ClockRewind} label="Versions" className="numeric">
      {history ? (
        <Link
          href={historyHref(path)}
          data-versions-line
          className="rounded-sm text-foreground underline-offset-2 focus-ring hover:underline"
        >
          {versionsSentence(history, whenAgo)}
        </Link>
      ) : (
        <span className="text-muted-foreground">{failed ? "Could not be read" : "Reading…"}</span>
      )}
    </Detail>
  )
}

"use client"

import { useEffect, useMemo, useState } from "react"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FilePreview } from "@/lib/types"
import { LoadingRows } from "@/components/state"
import { Well } from "@/components/panel"
import { rawUrl } from "@/components/files/media"

/**
 * The pieces a preview is made of, shared by the inspector beside the listing
 * and the full-screen viewer: the two show the same head of a text file, the
 * same PDF and the same archive listing, at different sizes.
 */

/** The head, with line numbers, because "line 42" is how errors are reported. */
export function TextHead({ text, className }: { text: string; className?: string }) {
  const lines = useMemo(() => text.replace(/\n$/, "").split("\n"), [text])
  return (
    <div className={cn("grid grid-cols-[auto_1fr] gap-x-3 p-2", className)}>
      <div className="numeric shrink-0 text-right text-muted-foreground/60 select-none">
        {lines.map((_, i) => (
          <div key={i}>{i + 1}</div>
        ))}
      </div>
      <div className="min-w-0">
        {lines.map((line, i) => (
          <div key={i} className="whitespace-pre">
            {line || " "}
          </div>
        ))}
      </div>
    </div>
  )
}

/**
 * A PDF through a blob rather than straight from the API.
 *
 * The API's own responses carry `X-Frame-Options: DENY`, which is what keeps
 * the dashboard out of somebody else's iframe and which a browser applies to
 * the PDF viewer too. Fetching the bytes and framing a blob URL keeps that
 * header exactly as strict as it is and still shows the document.
 */
export function PdfPreview({
  path,
  modified,
  className,
}: {
  path: string
  modified: string
  className?: string
}) {
  const [url, setUrl] = useState<string>()
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let objectUrl: string | undefined
    const controller = new AbortController()
    fetch(rawUrl(path, modified), { credentials: "include", signal: controller.signal })
      .then((res) => (res.ok ? res.blob() : Promise.reject(new Error(res.statusText))))
      .then((blob) => {
        objectUrl = URL.createObjectURL(blob)
        setUrl(objectUrl)
      })
      .catch(() => !controller.signal.aborted && setFailed(true))
    return () => {
      controller.abort()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [path, modified])

  if (failed) {
    return (
      <p className="rounded-lg border border-dashed border-hairline p-4 text-center text-xs text-muted-foreground">
        This PDF could not be loaded for preview. Download it to read it.
      </p>
    )
  }
  if (!url) return <LoadingRows rows={2} />
  return (
    <iframe
      src={url}
      title="PDF preview"
      className={cn("w-full rounded-lg border border-hairline", className ?? "h-72")}
    />
  )
}

/** What is inside an archive, from the peek the server took without unpacking it. */
export function ArchiveListing({
  preview,
  className,
}: {
  preview: FilePreview
  className?: string
}) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <p className="text-hint text-muted-foreground">
        {preview.archiveError
          ? `Could not read the archive: ${preview.archiveError}`
          : preview.entries?.length
            ? `${preview.entryCount}${preview.moreEntries ? "+" : ""} entries inside`
            : "This archive's format cannot be listed here — download it to unpack."}
      </p>
      {preview.entries && preview.entries.length > 0 && (
        <Well className="max-h-64 space-y-0.5 p-2">
          {preview.entries.map((item) => (
            <div key={item.name} className="flex items-center justify-between gap-3">
              <span className="truncate" title={item.name}>
                {item.name}
              </span>
              {!item.isDir && (
                <span className="numeric shrink-0 text-muted-foreground">{bytes(item.size)}</span>
              )}
            </div>
          ))}
        </Well>
      )}
    </div>
  )
}

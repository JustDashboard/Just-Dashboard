"use client"

import { useState } from "react"
import { Download, Video } from "@/components/icons"
import { downloadUrl } from "@/lib/api"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { extensionOf, rawUrl } from "@/components/files/media"

/**
 * A video, or a sentence saying this browser cannot play it.
 *
 * The raw route serves `.mkv` and `.mov` because some browsers play some of
 * them; most of the time a Matroska file or an iPhone's HEVC recording is a
 * black box with a seek bar that never moves. The element says so — it fails
 * to load, or loads a soundtrack with no picture — and this turns that into a
 * sentence and a Download, rather than a box that looks broken. Nothing is
 * converted: there is no ffmpeg in this stack.
 */
export function PlayableVideo({
  path,
  name,
  modified,
  autoPlay,
  className,
  videoClassName,
}: {
  path: string
  name: string
  modified: string
  autoPlay?: boolean
  className?: string
  videoClassName?: string
}) {
  const [cannot, setCannot] = useState<"play" | "picture" | null>(null)
  if (cannot) {
    const ext = extensionOf(name)
    return (
      <div
        data-cannot-play
        className={cn("flex flex-col items-center justify-center gap-3 p-4 text-center", className)}
      >
        <Video aria-hidden className="size-8 text-muted-foreground" />
        <p className="max-w-sm text-body">
          {cannot === "play"
            ? `This browser cannot play this video${ext ? ` (.${ext})` : ""}.`
            : `This browser plays this video's sound but cannot draw its picture${ext ? ` (.${ext})` : ""}.`}{" "}
          Download it to watch it in a player on your computer.
        </p>
        <Button size="sm" variant="outline" asChild>
          <a href={downloadUrl("/files/download", { path })} download>
            <Download className="size-3.5" />
            Download
          </a>
        </Button>
      </div>
    )
  }
  return (
    <video
      src={rawUrl(path, modified)}
      controls
      autoPlay={autoPlay}
      preload="metadata"
      onError={() => setCannot("play")}
      onLoadedMetadata={(e) => {
        // A codec the browser cannot decode often still yields its audio
        // track: the metadata arrives with no picture size at all.
        if (e.currentTarget.videoWidth === 0 && e.currentTarget.videoHeight === 0) {
          setCannot("picture")
        }
      }}
      className={videoClassName}
    />
  )
}

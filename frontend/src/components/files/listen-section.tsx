"use client"

import { Play } from "@/components/icons"
import type { FileEntry } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { PaneSection } from "@/components/panel"
import { rawUrl } from "@/components/files/media"
import { PlayerControls, useAudioQueue } from "@/components/files/audio-queue"
import { clockTime } from "@/components/files/gallery"

/**
 * The details pane's "Listen" for a track: Play queues it with the rest of
 * its folder through the page's player, and while it is the one playing the
 * pane has the player's controls and where it is. Outside a Files page there
 * is no queue, and the track is its own element, as it always was.
 */
export function ListenSection({ entry, modified }: { entry: FileEntry; modified: string }) {
  const queue = useAudioQueue()
  const track = queue?.tracks[queue.index]
  return (
    <PaneSection title="Listen">
      {!queue ? (
        <audio src={rawUrl(entry.path, modified)} controls className="w-full" />
      ) : track?.path === entry.path ? (
        <div className="flex items-center gap-2">
          <PlayerControls queue={queue} />
          <span className="numeric text-hint text-muted-foreground">
            {clockTime(queue.position)}
            {queue.duration > 0 && ` of ${clockTime(queue.duration)}`}
          </span>
        </div>
      ) : (
        <Button size="sm" variant="outline" onClick={() => queue.playFrom(entry)}>
          <Play className="size-3.5" />
          Play
        </Button>
      )}
    </PaneSection>
  )
}

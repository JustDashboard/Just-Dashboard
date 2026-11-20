"use client"

import { useEffect, useRef, useSyncExternalStore } from "react"
import { Cross, Music, Pause, Play, TrackNext, TrackPrevious } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"
import { IconAction } from "@/components/icon-action"
import { rawUrl } from "@/components/files/media"
import { clockTime, queueFrom, type Track } from "@/components/files/gallery"

/**
 * Audio in Files plays as a queue.
 *
 * Playing a track queues the rest of its folder after it, in the listing's
 * order, and the queue is the page's rather than the viewer's: closing the
 * viewer or walking into another folder leaves the music playing, with a
 * player of one line in the listing's foot to go back, on, or stop. There is
 * one `<audio>` element for the whole page (`AudioQueueHost`), so a second
 * track never plays over the first.
 *
 * It is a small store rather than a context so the page mounts it with one
 * element, and so the quarter-second position updates draw only the player
 * and the viewer's stage, never the listing.
 */

type State = {
  tracks: Track[]
  index: number
  playing: boolean
  position: number
  duration: number
}

const EMPTY: State = { tracks: [], index: 0, playing: false, position: 0, duration: 0 }

let state: State = EMPTY
let hosted = false
let element: HTMLAudioElement | null = null
let folder: FileEntry[] = []
const listeners = new Set<() => void>()

function set(next: Partial<State>) {
  state = { ...state, ...next }
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

const current = () => state.tracks[state.index]

const actions = {
  /**
   * Queues the audio files of `entries` (the browsed folder's, when not given)
   * and plays from `entry`, or carries on if it is the track already playing.
   */
  playFrom(entry: Pick<FileEntry, "path" | "name" | "modified">, entries?: FileEntry[]) {
    if (current()?.path === entry.path) {
      void element?.play().catch(() => undefined)
      return
    }
    const { tracks, index } = queueFrom(entry, entries ?? folder)
    set({ tracks, index, position: 0, duration: 0 })
  },
  toggle() {
    if (!element) return
    if (element.paused) void element.play().catch(() => undefined)
    else element.pause()
  },
  step(delta: number) {
    const next = state.index + delta
    if (next < 0 || next >= state.tracks.length) return
    set({ index: next, position: 0, duration: 0 })
  },
  seek(seconds: number) {
    if (element) element.currentTime = seconds
  },
  stop() {
    element?.pause()
    set(EMPTY)
  },
}

export type Queue = State & typeof actions

const snapshot = () => (hosted ? state : null)

/** The page's audio queue, or nothing where no Files page hosts one. */
export function useAudioQueue(): Queue | null {
  const now = useSyncExternalStore(subscribe, snapshot, () => null)
  return now ? { ...now, ...actions } : null
}

/**
 * The queue's one `<audio>` element, mounted once by the Files page, which
 * hands it the browsed folder's entries: what a track pressed in the details
 * pane queues after it. Leaving the page stops the music.
 */
export function AudioQueueHost({ entries }: { entries: FileEntry[] }) {
  const audio = useRef<HTMLAudioElement>(null)
  const queue = useAudioQueue()
  const track = queue?.tracks[queue.index]

  useEffect(() => {
    folder = entries
  }, [entries])

  useEffect(() => {
    element = audio.current
    hosted = true
    set({})
    return () => {
      element?.pause()
      element = null
      hosted = false
      state = EMPTY
      for (const listener of listeners) listener()
    }
  }, [])

  // A new track starts as soon as its address is set.
  useEffect(() => {
    if (track) void audio.current?.play().catch(() => set({ playing: false }))
  }, [track])

  // The keyboard's and the lock screen's media keys reach the queue too.
  const index = queue?.index ?? 0
  const count = queue?.tracks.length ?? 0
  useEffect(() => {
    if (!track || typeof navigator === "undefined" || !("mediaSession" in navigator)) return
    const session = navigator.mediaSession
    session.metadata = new MediaMetadata({ title: track.name, album: track.folder })
    session.setActionHandler("previoustrack", index > 0 ? () => actions.step(-1) : null)
    session.setActionHandler("nexttrack", index < count - 1 ? () => actions.step(1) : null)
    return () => {
      session.setActionHandler("previoustrack", null)
      session.setActionHandler("nexttrack", null)
    }
  }, [track, index, count])

  return (
    <audio
      ref={audio}
      data-files-audio
      src={track ? rawUrl(track.path, track.modified) : undefined}
      preload="auto"
      hidden
      onPlay={() => set({ playing: true })}
      onPause={() => set({ playing: false })}
      onTimeUpdate={(e) => set({ position: e.currentTarget.currentTime })}
      onDurationChange={(e) =>
        set({ duration: Number.isFinite(e.currentTarget.duration) ? e.currentTarget.duration : 0 })
      }
      onEnded={() =>
        state.index < state.tracks.length - 1 ? actions.step(1) : set({ playing: false })
      }
      // A track this browser cannot decode is skipped rather than ending the queue.
      onError={() => {
        if (track && state.index < state.tracks.length - 1) actions.step(1)
      }}
    />
  )
}

/**
 * The player: one line in the listing's foot while something is queued — the
 * track's name, where it is, and back, play or pause, on and stop. It sits in
 * the foot the listing already has, so it never moves a row; on a phone it is
 * the whole foot while it plays.
 */
export function PlayerLine({ className }: { className?: string }) {
  const queue = useAudioQueue()
  const track = queue?.tracks[queue.index]
  if (!queue || !track) return null
  return (
    <div
      data-files-player
      role="group"
      aria-label="Playing"
      className={cn(
        "flex h-6 min-w-0 flex-1 items-center gap-1.5 text-hint text-muted-foreground",
        // A phone's foot has room for one thing: while music plays, it is this.
        "max-sm:absolute max-sm:inset-0 max-sm:z-raised max-sm:h-auto max-sm:bg-surface-header max-sm:px-2",
        className,
      )}
    >
      <Music aria-hidden className="size-3.5 shrink-0" />
      <PlayerControls queue={queue} />
      <span className="min-w-0 truncate text-foreground" title={track.path}>
        {track.name}
      </span>
      <span className="numeric shrink-0">
        {clockTime(queue.position)}
        {queue.duration > 0 && ` of ${clockTime(queue.duration)}`}
      </span>
      {queue.tracks.length > 1 && (
        <span className="numeric shrink-0 max-md:hidden">
          · {queue.index + 1} of {queue.tracks.length}
        </span>
      )}
      <IconAction label="Stop playing" className="ml-auto size-6 shrink-0" onClick={queue.stop}>
        <Cross />
      </IconAction>
    </div>
  )
}

/** Back, play or pause, on: the same three in the foot and on the viewer's stage. */
export function PlayerControls({ queue, large }: { queue: Queue; large?: boolean }) {
  const size = large ? "size-9" : "size-6"
  return (
    <span className="flex shrink-0 items-center gap-0.5">
      <IconAction
        label="Previous track"
        className={size}
        disabled={queue.index === 0}
        onClick={() => queue.step(-1)}
      >
        <TrackPrevious />
      </IconAction>
      <IconAction
        label={queue.playing ? "Pause" : "Play"}
        variant={large ? "outline" : "ghost"}
        className={size}
        onClick={queue.toggle}
      >
        {queue.playing ? <Pause /> : <Play />}
      </IconAction>
      <IconAction
        label="Next track"
        className={size}
        disabled={queue.index >= queue.tracks.length - 1}
        onClick={() => queue.step(1)}
      >
        <TrackNext />
      </IconAction>
    </span>
  )
}

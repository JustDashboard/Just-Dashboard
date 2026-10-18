import type { ControlStats, StreamStats } from "@/lib/remote-desktop"

/**
 * What a session measures of itself, read off the pieces that carry it
 * rather than asked of the server: the Guacamole tunnel's own frame marks
 * and the stream's video element. Neither mode said how it was doing, so
 * a session that felt slow could not say whether the server, the path or
 * this browser was slow.
 */

/** The two members of Guacamole's tunnel a session's frames pass through. */
export type MeteredTunnel = {
  oninstruction: null | ((opcode: string, args: unknown[]) => void)
  sendMessage: (...messages: unknown[]) => void
}

/**
 * Counts guacd's frames as they arrive and as the browser finishes drawing
 * them. guacd ends every frame with a `sync` carrying its clock, and the
 * client answers the same `sync` once every image in the frame is drawn;
 * the time between the two is what guacd itself calls the client's
 * processing lag, and what it slows down for. Call read once a second.
 */
export function meterTunnel(tunnel: MeteredTunnel, now: () => number = () => performance.now()) {
  const pending = new Map<string, number>()
  let received = 0
  let answered = 0
  let lagTotal = 0
  let lagCount = 0
  let since = now()
  const inner = tunnel.oninstruction
  tunnel.oninstruction = (opcode, args) => {
    if (opcode === "sync") {
      received += 1
      pending.set(String(args[0]), now())
    }
    inner?.(opcode, args)
  }
  const send = tunnel.sendMessage.bind(tunnel)
  tunnel.sendMessage = (...messages: unknown[]) => {
    if (messages[0] === "sync") {
      const arrived = pending.get(String(messages[1]))
      if (arrived !== undefined) {
        pending.delete(String(messages[1]))
        answered += 1
        lagTotal += now() - arrived
        lagCount += 1
      }
    }
    send(...messages)
  }
  return {
    read(): ControlStats {
      const at = now()
      const seconds = Math.max((at - since) / 1000, 0.001)
      const stats = {
        serverFps: Math.round(received / seconds),
        clientFps: Math.round(answered / seconds),
        lagMs: lagCount ? Math.round(lagTotal / lagCount) : 0,
      }
      received = answered = lagTotal = lagCount = 0
      since = at
      // A frame the client never answers would otherwise be kept for ever.
      for (const [key, arrived] of pending) if (at - arrived > 10_000) pending.delete(key)
      return stats
    },
  }
}

type FrameMetadata = {
  expectedDisplayTime: number
  receiveTime?: number
}

/** The part of a video element a stream's frames can be counted on. */
export type MeteredVideo = {
  requestVideoFrameCallback?: (callback: (now: number, metadata: FrameMetadata) => void) => number
  cancelVideoFrameCallback?: (handle: number) => void
  getVideoPlaybackQuality?: () => { droppedVideoFrames: number }
}

/**
 * Counts a stream's frames as the browser shows them, and over WebRTC how
 * long each waited between its last packet and the screen: the browser's
 * own share of the delay, which is the share a setting here can change.
 * report is called about once a second; stop ends it.
 */
export function meterVideo(
  video: MeteredVideo,
  report: (stats: StreamStats) => void,
  now: () => number = () => performance.now(),
) {
  if (!video.requestVideoFrameCallback) return () => {}
  let frames = 0
  let delayTotal = 0
  let delayCount = 0
  let since = now()
  let handle = 0
  let stopped = false
  const onFrame = (_time: number, metadata: FrameMetadata) => {
    if (stopped) return
    frames += 1
    if (metadata.receiveTime !== undefined) {
      delayTotal += Math.max(metadata.expectedDisplayTime - metadata.receiveTime, 0)
      delayCount += 1
    }
    const at = now()
    if (at - since >= 1000) {
      report({
        fps: Math.round((frames * 1000) / (at - since)),
        delayMs: delayCount ? Math.round(delayTotal / delayCount) : undefined,
        dropped: video.getVideoPlaybackQuality?.().droppedVideoFrames ?? 0,
      })
      frames = delayTotal = delayCount = 0
      since = at
    }
    handle = video.requestVideoFrameCallback!(onFrame)
  }
  handle = video.requestVideoFrameCallback(onFrame)
  return () => {
    stopped = true
    video.cancelVideoFrameCallback?.(handle)
  }
}

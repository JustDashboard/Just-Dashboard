import { latency, type RdComputerView, type Reachability } from "@/lib/remote-desktop"

/**
 * What a computer's row on the Remote Desktop page says in words, apart from
 * any drawing: its state in a word or two, how far it is and when it was last
 * used as one line, and where the dashboard signs in.
 */

/**
 * The state as the row's own column says it. The protocol is on the line
 * beside it, so a port that does not answer is "Not answering" here and the
 * full sentence (`reach.label`, with the probe's reason) is the tooltip.
 */
export function stateWord(reach: Reachability) {
  return reach.kind === "closed" ? "Not answering" : reach.label
}

/** The tailnet's path in a lower-case word, for the middle of a line. */
export function pathWord(computer: Pick<RdComputerView, "peer">) {
  if (!computer.peer) return "network"
  if (computer.peer.direct) return "direct"
  return computer.peer.relay ? `relay ${computer.peer.relay}` : "tailnet"
}

/**
 * How long ago, to the largest unit only: a row is read for "minutes or
 * days", and a figure that counts seconds changes under the eye at every
 * poll.
 */
export function agoWord(unix: number, now = Date.now()) {
  const seconds = Math.max(0, Math.round(now / 1000 - unix))
  if (seconds < 60) return "just now"
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

/**
 * "43 ms direct · used 11m ago": the round trip and the path while the
 * computer answers, then when a session to it last ended or began. A computer
 * that is off has no round trip to say, and one never opened says so.
 */
export function readingLine(
  computer: Pick<RdComputerView, "peer" | "reach" | "lastSessionAt">,
  reach: Reachability,
  now = Date.now(),
) {
  const trip = latency(computer.reach.latencyMs)
  const far =
    reach.kind === "offline" || reach.kind === "closed"
      ? undefined
      : trip
        ? `${trip} ${pathWord(computer)}`
        : pathWord(computer)
  const used = computer.lastSessionAt
    ? `used ${agoWord(computer.lastSessionAt, now)}`
    : "never used"
  return [far, used].filter(Boolean).join(" · ")
}

/**
 * Where the dashboard signs in, the way a shell writes it: `wayy@office-pc:3389`.
 * Without a saved account it is the address alone.
 */
export function signInAddress(computer: Pick<RdComputerView, "username" | "host" | "port">) {
  const at = `${computer.host}:${computer.port}`
  return computer.username ? `${computer.username}@${at}` : at
}

/** What a session to this computer will have to ask for, because nothing is saved. */
export function signInGap(computer: Pick<RdComputerView, "username" | "hasPassword" | "protocol">) {
  const gaps = [
    !computer.username && computer.protocol === "rdp" ? "no account saved" : undefined,
    !computer.hasPassword ? "no password saved" : undefined,
  ].filter(Boolean)
  return gaps.length ? gaps.join(", ") : undefined
}

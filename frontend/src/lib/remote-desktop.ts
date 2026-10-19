import { API_BASE } from "@/lib/api"
import { LANES } from "@/lib/hue"
import { plural } from "@/lib/format"

/**
 * Remote Desktop's data and the decisions drawn from it, apart from any
 * drawing: what the backend's `/remote-desktop` routes return, which hue a
 * computer is drawn in, how reachable it is in a word, what the history of
 * sessions adds up to, and what a refused session means in the reader's
 * terms rather than Guacamole's status codes.
 */

export type RdOS = "windows" | "macos" | "linux"
export type RdProtocol = "rdp" | "vnc"

export type RdComputer = {
  id: string
  name: string
  os: RdOS
  host: string
  protocol: RdProtocol
  port: number
  username: string
  domain: string
  hasPassword: boolean
  security: string
  layout: string
  peerId: string
  createdAt: number
  updatedAt: number
  lastSessionAt: number
  /** The stream bridge's id for the computer once it is paired with Sunshine; 0 before. */
  streamHostId: number
  /**
   * Whether RDP's Graphics Pipeline is asked for. Off, guacd draws on the
   * computer's own frame marks, which is smoother and lighter; on, for the
   * few servers that refuse to connect without it.
   */
  gfx: boolean
}

export type RdReach = {
  state: "open" | "closed" | "offline"
  latencyMs?: number
  error?: string
  checkedAt: number
}

export type RdPeer = {
  id: string
  name: string
  dnsName: string
  os: string
  address: string
  online: boolean
  direct: boolean
  relay: string
  lastSeen: number
}

export type RdComputerView = RdComputer & { peer?: RdPeer; reach: RdReach; live: number }

export type RdEngine = {
  state: "missing" | "running" | "stopped" | "unavailable" | "foreign"
  image: string
  version: string
  pulled: boolean
  address?: string
  container?: string
  /** Where an engine's WebRTC ports are published, as address:first-last. */
  media?: string
  startedAt?: number
  error?: string
}

export type RdTailnet = { installed: boolean; self?: RdPeer; peers: RdPeer[]; error?: string }

export type RdSession = {
  id: number
  computerId: string
  actor: string
  startedAt: number
  endedAt: number
  outcome: string
}

export type RdOverview = {
  engine: RdEngine
  /** The stream bridge, Moonlight Web, beside the engine. */
  stream: RdEngine
  tailnet: RdTailnet
  computers: RdComputerView[]
  sessions: RdSession[]
  layouts: string[]
}

export type RdComputerInput = {
  name: string
  os: RdOS
  host: string
  protocol: RdProtocol
  port: number
  username: string
  domain: string
  password?: string
  security: string
  layout: string
  peerId: string
  gfx: boolean
}

export type RdStreamApp = { id: number; title: string }

/** A computer's stream: the bridge, Sunshine's port on the computer, and its pairing. */
export type RdStream = {
  bridge: RdEngine
  /** How this browser can be sent the picture: WebRTC over the tailnet, or the dashboard's WebSocket. */
  transport: "webrtc" | "websocket"
  address?: string
  sunshine: RdReach
  hostId: number
  paired: boolean
  pairing: boolean
  pin?: string
  error?: string
  apps: RdStreamApp[]
}

export const REMOTE_DESKTOP_HREF = "/remote-desktop"

export function sessionHref(id: string) {
  return `${REMOTE_DESKTOP_HREF}/${id}`
}

/** The unix seconds the backend sends, as the ISO time `relativeTime` reads. */
export function isoOf(unix: number | undefined) {
  return unix ? new Date(unix * 1000).toISOString() : undefined
}

/** The product logo a computer is drawn as. */
export function osProduct(os: RdOS) {
  if (os === "macos") return "apple"
  return os
}

export function osLabel(os: RdOS) {
  if (os === "macos") return "macOS"
  if (os === "linux") return "Linux"
  return "Windows"
}

/** What the reader calls the server on the computer, in its own system's words. */
export function protocolLabel(protocol: RdProtocol, os: RdOS) {
  if (protocol === "rdp") return "Remote Desktop"
  return os === "macos" ? "Screen Sharing" : "VNC"
}

export function defaultPort(protocol: RdProtocol) {
  return protocol === "vnc" ? 5900 : 3389
}

/**
 * The system a tailnet device says it runs, as one Remote Desktop can reach;
 * a phone or a tablet has no desktop to share and is left out.
 */
export function peerOS(os: string): RdOS | undefined {
  const lower = os.toLowerCase()
  if (lower === "windows") return "windows"
  if (lower === "macos" || lower === "darwin") return "macos"
  if (lower === "linux") return "linux"
  return undefined
}

/** The short name a tailnet device answers to: its MagicDNS label, else its host name. */
export function peerName(peer: Pick<RdPeer, "dnsName" | "name">) {
  const label = peer.dnsName.split(".")[0]
  return label || peer.name.toLowerCase()
}

/**
 * The tailnet's desktops not added yet, online ones first: what the add flow
 * offers and what the overview lists under the computers.
 */
export function candidates(tailnet: RdTailnet, computers: readonly RdComputer[]) {
  const taken = new Set(computers.map((computer) => computer.peerId).filter(Boolean))
  const hosts = new Set(computers.map((computer) => computer.host.toLowerCase()))
  return tailnet.peers
    .filter((peer) => peerOS(peer.os) !== undefined)
    .filter((peer) => !taken.has(peer.id) && !hosts.has(peer.address) && !hosts.has(peerName(peer)))
    .sort(
      (a, b) =>
        Number(b.online) - Number(a.online) ||
        b.lastSeen - a.lastSeen ||
        peerName(a).localeCompare(peerName(b)),
    )
}

/** The lane palette without slate, which a bar uses for "everything else". */
export const COMPUTER_HUES = LANES.filter((hue) => hue !== "var(--tag-slate)")

/**
 * Each computer's hue, dealt in the order the computers were added so a new
 * one never repaints the others: its card's edge, its wire, its key in a row.
 */
export function computerHues(computers: readonly Pick<RdComputer, "id" | "createdAt">[]) {
  const order = [...computers].sort((a, b) => a.createdAt - b.createdAt || a.id.localeCompare(b.id))
  return new Map(order.map((c, index) => [c.id, COMPUTER_HUES[index % COMPUTER_HUES.length]]))
}

export type Reachability = {
  kind: "live" | "open" | "closed" | "offline"
  tone: "success" | "warning" | "danger" | "neutral"
  label: string
}

/** A computer's state in the words a status dot and a wire are drawn from. */
export function reachability(view: RdComputerView): Reachability {
  if (view.live > 0) return { kind: "live", tone: "success", label: "In a session" }
  if (view.reach.state === "open") return { kind: "open", tone: "success", label: "Ready" }
  if (view.reach.state === "offline") return { kind: "offline", tone: "neutral", label: "Offline" }
  const port = `${protocolLabel(view.protocol, view.os)} is not answering`
  if (view.reach.error === "refused") return { kind: "closed", tone: "warning", label: port }
  return {
    kind: "closed",
    tone: "warning",
    label: view.reach.error ? `${port} (${view.reach.error})` : port,
  }
}

export function latency(ms: number | undefined) {
  if (ms === undefined) return undefined
  return ms < 10 ? `${ms.toFixed(1)} ms` : `${Math.round(ms)} ms`
}

/** The identity line's verdict: the one thing to know about the whole page. */
export function overviewVerdict(o: RdOverview) {
  if (o.engine.state !== "running") return { tone: "warning" as const, label: "Engine not running" }
  if (o.computers.length === 0) return undefined
  const live = o.computers.reduce((sum, c) => sum + c.live, 0)
  if (live > 0)
    return { tone: "success" as const, label: `${plural(live, "session")} open`, live: true }
  const ready = o.computers.filter((c) => c.reach.state === "open").length
  if (ready === o.computers.length)
    return { tone: "success" as const, label: `All ${o.computers.length} ready` }
  return {
    tone: ready === 0 ? ("warning" as const) : ("neutral" as const),
    label: `${ready} of ${o.computers.length} ready`,
  }
}

/**
 * Guacamole's status codes, and the messages guacd writes beside them, said
 * as what the reader can do about them. A code the table does not know keeps
 * guacd's own message.
 */
export function sessionProblem(
  code: number,
  message: string,
  computer: Pick<RdComputer, "name" | "port" | "protocol" | "os"> & { reach?: RdReach },
) {
  const server = protocolLabel(computer.protocol, computer.os)
  switch (code) {
    case 0x0207:
    case 0x0208:
      // guacd says "not found" for a sign-in it lost as much as for a port
      // nobody answers; the probe says which this was.
      if (computer.reach?.state === "open")
        return {
          title: `${computer.name} would not let the dashboard in`,
          detail:
            computer.os === "macos"
              ? `Screen Sharing answered, but the sign-in failed. Check the Mac account name and login password saved for ${computer.name}, and that the account is allowed under Screen Sharing › Allow access for.`
              : `${server} answered, but the connection failed. Check the username and password saved for ${computer.name}, and that the account may sign in remotely.`,
        }
      return {
        title: `${computer.name} is not answering`,
        detail: `Nothing answered on port ${computer.port}. Check that the computer is on and awake and that ${server} is turned on.`,
      }
    case 0x0202:
      return {
        title: `${computer.name} did not answer in time`,
        detail: "The computer may be asleep or busy. Wake it and try again.",
      }
    case 0x0301:
    case 0x0303:
      return {
        title: "The computer refused the sign-in",
        detail:
          computer.os === "macos"
            ? `Check the Mac account name and login password saved for ${computer.name}.`
            : `Check the username and password saved for ${computer.name}.`,
      }
    case 0x0209:
      return {
        title: "Someone else took over the session",
        detail: "Another connection to this computer replaced this one.",
      }
    case 0x020a:
    case 0x020b:
      return { title: "The session ended", detail: "The computer closed it." }
  }
  return { title: "The session could not continue", detail: message || "guacd gave no reason." }
}

/**
 * How many of the screen's pixels a Control session asks for per pixel of
 * the frame. Smooth is one: a 1440 px frame is a 1440 px screen. Sharp is
 * the browser's own density, capped at two: the same frame on a 2x display
 * is a 2880 px screen, four times the pixels for the server to encode and
 * the browser to decode, and that many fewer frames a second.
 */
export type ControlDensity = "smooth" | "sharp"

/**
 * The size to ask the remote screen to be: the frame's own size, in its
 * device pixels only when asked for sharpness, and never more than twice
 * the CSS size so a 3x phone does not ask a PC for a screen three times the
 * bandwidth.
 */
export function displaySize(width: number, height: number, ratio: number, sharp = false) {
  const density = sharp ? Math.min(Math.max(ratio || 1, 1), 2) : 1
  return {
    width: Math.max(320, Math.round(width * density)),
    height: Math.max(240, Math.round(height * density)),
    dpi: Math.round(96 * density),
    density,
  }
}

/** The tailnet's path to a computer, in a word, when the tailnet knows it. */
export function pathLabel(peer: Pick<RdPeer, "direct" | "relay"> | undefined) {
  if (!peer) return undefined
  if (peer.direct) return "direct"
  if (peer.relay) return `relayed via ${peer.relay}`
  return undefined
}

/**
 * What a Control session measures of itself each second: the frames guacd
 * sent, the frames this browser finished drawing, and how long each took
 * to draw once it arrived. guacd slows down and lowers its picture quality
 * when that lag grows, so a high lag is the browser holding the session
 * back and a low client rate with a low lag is the server or the path.
 */
export type ControlStats = { serverFps: number; clientFps: number; lagMs: number }

/**
 * What a stream measures of itself each second: frames shown, the time
 * from a frame's last packet arriving to its being shown (known over
 * WebRTC only), and frames dropped since the stream began.
 */
export type StreamStats = { fps: number; delayMs?: number; dropped: number }

/** X11 keysyms for the combinations a browser keeps for itself. */
export const KEYSYM = {
  control: 0xffe3,
  alt: 0xffe9,
  shift: 0xffe1,
  super: 0xffeb,
  delete: 0xffff,
  tab: 0xff09,
  escape: 0xff1b,
  print: 0xff61,
  l: 0x006c,
} as const

export type KeyCombo = { key: string; label: string; keys: number[]; os?: RdOS[] }

/** The combinations the toolbar can send, held down in order and let go in reverse. */
export const KEY_COMBOS: KeyCombo[] = [
  {
    key: "cad",
    label: "Ctrl+Alt+Del",
    keys: [KEYSYM.control, KEYSYM.alt, KEYSYM.delete],
    os: ["windows"],
  },
  { key: "win", label: "Windows key", keys: [KEYSYM.super], os: ["windows"] },
  { key: "alt-tab", label: "Alt+Tab", keys: [KEYSYM.alt, KEYSYM.tab], os: ["windows", "linux"] },
  { key: "lock", label: "Lock the screen", keys: [KEYSYM.super, KEYSYM.l], os: ["windows"] },
  { key: "print", label: "Print Screen", keys: [KEYSYM.print], os: ["windows", "linux"] },
  { key: "escape", label: "Esc", keys: [KEYSYM.escape] },
]

export function combosFor(os: RdOS) {
  return KEY_COMBOS.filter((combo) => !combo.os || combo.os.includes(os))
}

/**
 * How a session draws the computer. Control is guacd's RDP or VNC, which
 * works with nothing installed on the computer; Stream is Sunshine's video
 * through the bridge, smooth at the computer's own frame rate.
 */
export type SessionMode = "control" | "stream"

export function sessionMode(raw: string | null | undefined): SessionMode {
  return raw === "stream" ? "stream" : "control"
}

/**
 * The mode a computer opens in when the address names none. A Mac that is
 * paired with Sunshine opens in Stream: Apple's Screen Sharing serves
 * anything but Apple's own client a few frames a second, so Control on a
 * Mac is for signing in and setting up. Everything else opens in Control,
 * which needs nothing installed.
 */
export function defaultSessionMode(computer: Pick<RdComputer, "os" | "streamHostId">): SessionMode {
  return computer.os === "macos" && computer.streamHostId > 0 ? "stream" : "control"
}

/** Sunshine's port for pairing and the list of what it streams. */
export const SUNSHINE_PORT = 47989

export type StreamStage = "bridge" | "sunshine" | "pair" | "pin" | "ready"

/** The one thing a stream waits on, in the order they have to happen. */
export function streamStage(stream: RdStream): StreamStage {
  if (stream.bridge.state !== "running") return "bridge"
  if (stream.paired && stream.sunshine.state === "open") return "ready"
  if (stream.pairing) return "pin"
  if (stream.sunshine.state !== "open") return "sunshine"
  return "pair"
}

/** The app that is the computer's own screen: Sunshine calls it Desktop. */
export function desktopApp(apps: readonly RdStreamApp[]) {
  return apps.find((app) => app.title.toLowerCase() === "desktop") ?? apps[0]
}

/** The bridge's stream page, served through the dashboard's own origin. */
export function streamPageUrl(hostId: number, appId: number) {
  const query = new URLSearchParams({ hostId: String(hostId), appId: String(appId) })
  return `${API_BASE}/remote-desktop/bridge/stream.html?${query}`
}

export type StreamQuality = "light" | "smooth" | "sharp" | "max"

export const STREAM_QUALITIES: {
  key: StreamQuality
  label: string
  size: string
  height: number
  fps: number
  bitrate: number
}[] = [
  { key: "light", label: "Light", size: "720p", height: 720, fps: 60, bitrate: 8_000 },
  { key: "smooth", label: "Smooth", size: "1080p", height: 1080, fps: 60, bitrate: 20_000 },
  { key: "sharp", label: "Sharp", size: "1440p", height: 1440, fps: 60, bitrate: 40_000 },
  // Twice the frames, each one half as old, and twice the bits for them. A
  // computer whose screen runs at 60 Hz sends 60; the bits still go to the
  // picture.
  { key: "max", label: "Max", size: "1440p", height: 1440, fps: 120, bitrate: 80_000 },
]

export function streamQuality(quality: StreamQuality) {
  return STREAM_QUALITIES.find((q) => q.key === quality) ?? STREAM_QUALITIES[1]
}

/** A screen's width over its height, until the stream has shown the computer's own. */
export const WIDE_SCREEN = 16 / 9

/**
 * The size to ask Sunshine for: the quality's height at the screen's own
 * shape, in even pixels for the encoder. Sunshine cannot change a Mac's
 * resolution; it fits the screen into whatever it is asked for and pads the
 * rest black, so a 16:10 Mac asked for 1920×1080 came back pillarboxed.
 */
export function streamSize(quality: StreamQuality, shape = WIDE_SCREEN) {
  const { height } = streamQuality(quality)
  return { width: Math.round((height * shape) / 2) * 2, height }
}

/** How dark a pixel of Sunshine's padding is, after the encoder has been at it. */
const PADDING_LEVEL = 12

/**
 * The computer's screen's width over its height, read off one frame of its
 * stream: RGBA pixels, `width` by `height`, of a picture `aspect` wide.
 *
 * Sunshine pads evenly and on one axis only, so a frame lit to every edge is
 * the screen's own shape and one dark on both sides of one axis gives the
 * screen's shape away. Dark at a single edge, or on both axes, is the picture
 * being dark rather than padding, and says nothing.
 */
export function screenShape(
  pixels: Uint8ClampedArray,
  width: number,
  height: number,
  aspect = width / height,
) {
  const lit = (x: number, y: number) => {
    const at = (y * width + x) * 4
    return (
      pixels[at] > PADDING_LEVEL || pixels[at + 1] > PADDING_LEVEL || pixels[at + 2] > PADDING_LEVEL
    )
  }
  const litColumn = (x: number) => {
    for (let y = 0; y < height; y++) if (lit(x, y)) return true
    return false
  }
  const litRow = (y: number) => {
    for (let x = 0; x < width; x++) if (lit(x, y)) return true
    return false
  }
  let left = 0
  while (left < width && !litColumn(left)) left++
  if (left === width) return undefined
  let right = width - 1
  while (!litColumn(right)) right--
  let top = 0
  while (!litRow(top)) top++
  let bottom = height - 1
  while (!litRow(bottom)) bottom--

  // A sample is a scaled-down frame, whose edge pixels are blends.
  const slack = 2
  const bare = (near: number, far: number) => Math.max(near, far) <= slack
  const padded = (near: number, far: number) =>
    Math.min(near, far) > slack && Math.abs(near - far) <= slack
  const sides = [left, width - 1 - right] as const
  const ends = [top, height - 1 - bottom] as const
  const across = (right - left + 1) / width
  const down = (bottom - top + 1) / height
  if (bare(...sides) && bare(...ends)) return aspect
  if (padded(...sides) && bare(...ends)) return aspect * across
  if (padded(...ends) && bare(...sides)) return aspect / down
  return undefined
}

/** Where the bridge's stream page keeps its settings, in this origin's storage. */
export const BRIDGE_SETTINGS_KEY = "mlSettings"

/**
 * The bridge's settings for a stream, over whatever the reader changed in its
 * own menu. H.264, which every browser decodes in hardware; WebRTC when the
 * backend says this browser can reach the bridge's UDP ports ("auto" falls
 * back to the WebSocket should it still fail), else the dashboard's own
 * WebSocket; and the bridge's own frame queue of three. The bridge drops a
 * frame only past that many waiting, and asks Sunshine for a whole new
 * picture after one: a queue of one dropped frames on every hiccup and
 * spent the bitrate on keyframes, which was a rougher picture, not a
 * sooner one. The size is the quality's at the screen's own shape.
 */
export function bridgeSettings(
  held: Record<string, unknown>,
  quality: StreamQuality,
  transport: RdStream["transport"],
  shape?: number,
) {
  const preset = streamQuality(quality)
  return {
    ...held,
    dataTransport: transport === "webrtc" ? "auto" : "websocket",
    videoCodec: "h264",
    videoSize: "custom",
    videoSizeCustom: streamSize(quality, shape),
    fps: preset.fps,
    bitrate: preset.bitrate,
    videoFrameQueueSize: 3,
    playAudioLocal: false,
    enterFullscreenOnStreamStart: false,
  }
}

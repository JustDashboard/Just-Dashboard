import { describe, expect, test } from "bun:test"
import {
  bridgeSettings,
  defaultSessionMode,
  candidates,
  combosFor,
  computerHues,
  desktopApp,
  displaySize,
  pathLabel,
  overviewVerdict,
  peerOS,
  reachability,
  sessionMode,
  sessionProblem,
  screenShape,
  streamOverscan,
  streamPageUrl,
  streamStage,
} from "./remote-desktop"

const peer = (over) => ({
  id: "n1",
  name: "OFFICE-PC",
  dnsName: "office-pc.example.ts.net.",
  os: "windows",
  address: "100.101.12.7",
  online: true,
  direct: true,
  relay: "",
  lastSeen: 0,
  ...over,
})

const view = (over) => ({
  id: "a",
  name: "office-pc",
  os: "windows",
  host: "office-pc",
  protocol: "rdp",
  port: 3389,
  username: "",
  domain: "",
  hasPassword: true,
  security: "",
  layout: "",
  peerId: "",
  createdAt: 1,
  updatedAt: 1,
  lastSessionAt: 0,
  live: 0,
  reach: { state: "open", latencyMs: 43, checkedAt: 0 },
  ...over,
})

describe("candidates", () => {
  test("offers the tailnet's desktops not added yet, online first, and never a phone", () => {
    const tailnet = {
      installed: true,
      peers: [
        peer({
          id: "mac",
          name: "Studio",
          dnsName: "studio-mac.example.ts.net.",
          os: "macOS",
          online: false,
          lastSeen: 9,
        }),
        peer({ id: "phone", name: "localhost", dnsName: "iphone.example.ts.net.", os: "iOS" }),
        peer({
          id: "added",
          name: "GAMING-PC",
          dnsName: "gaming-pc.example.ts.net.",
          address: "100.101.12.8",
        }),
        peer({
          id: "pc",
          name: "WORKSHOP-PC",
          dnsName: "workshop-pc.example.ts.net.",
          address: "100.101.12.10",
        }),
      ],
    }
    const offered = candidates(tailnet, [view({ host: "100.101.12.8" })])
    expect(offered.map((p) => p.id)).toEqual(["pc", "mac"])
    expect(peerOS("darwin")).toBe("macos")
    expect(peerOS("android")).toBeUndefined()
  })
})

describe("computerHues", () => {
  test("deals hues in the order computers were added, so a new one moves nobody", () => {
    const first = computerHues([view({ id: "b", createdAt: 2 }), view({ id: "a", createdAt: 1 })])
    const later = computerHues([
      view({ id: "a", createdAt: 1 }),
      view({ id: "b", createdAt: 2 }),
      view({ id: "c", createdAt: 3 }),
    ])
    expect(later.get("a")).toBe(first.get("a"))
    expect(later.get("b")).toBe(first.get("b"))
    expect(new Set(later.values()).size).toBe(3)
    expect([...later.values()]).not.toContain("var(--tag-slate)")
  })
})

describe("reachability", () => {
  test("says a live session, a ready port, a refused one and an offline computer apart", () => {
    expect(reachability(view({ live: 1 })).kind).toBe("live")
    expect(reachability(view()).label).toBe("Ready")
    const refused = reachability(
      view({ reach: { state: "closed", error: "refused", checkedAt: 0 } }),
    )
    expect(refused).toEqual({
      kind: "closed",
      tone: "warning",
      label: "Remote Desktop is not answering",
    })
    const mac = view({
      os: "macos",
      protocol: "vnc",
      reach: { state: "closed", error: "no answer", checkedAt: 0 },
    })
    expect(reachability(mac).label).toBe("Screen Sharing is not answering (no answer)")
    expect(reachability(view({ reach: { state: "offline", checkedAt: 0 } })).tone).toBe("neutral")
  })
})

describe("overviewVerdict", () => {
  const overview = (computers, engine = "running") => ({
    engine: { state: engine },
    computers,
    tailnet: { installed: true, peers: [] },
    sessions: [],
    layouts: [],
  })
  test("puts a stopped engine before everything, then open sessions, then readiness", () => {
    expect(overviewVerdict(overview([view()], "missing")).label).toBe("Engine not running")
    expect(overviewVerdict(overview([view({ live: 2 })])).label).toBe("2 sessions open")
    expect(overviewVerdict(overview([view(), view({ id: "b" })])).label).toBe("All 2 ready")
    const half = overviewVerdict(
      overview([view(), view({ id: "b", reach: { state: "offline", checkedAt: 0 } })]),
    )
    expect(half).toEqual({ tone: "neutral", label: "1 of 2 ready" })
    expect(overviewVerdict(overview([]))).toBeUndefined()
  })
})

describe("sessionProblem", () => {
  test("turns Guacamole's codes into what to check on the computer", () => {
    const pc = { name: "office-pc", port: 3389, protocol: "rdp", os: "windows" }
    expect(sessionProblem(0x0207, "", pc).detail).toContain("Remote Desktop is turned on")
    const answered = { ...pc, os: "macos", protocol: "vnc", reach: { state: "open", checkedAt: 0 } }
    expect(sessionProblem(0x0207, "", answered).title).toBe(
      "office-pc would not let the dashboard in",
    )
    expect(sessionProblem(0x0207, "", answered).detail).toContain("Allow access for")
    expect(sessionProblem(0x0301, "", pc).title).toBe("The computer refused the sign-in")
    expect(sessionProblem(0x0200, "Aborted. See logs.", pc).detail).toBe("Aborted. See logs.")
  })
})

describe("displaySize", () => {
  test("asks for one pixel per pixel unless sharpness is asked for, then at most twice", () => {
    expect(displaySize(1000, 600, 1)).toEqual({ width: 1000, height: 600, dpi: 96, density: 1 })
    expect(displaySize(1000, 600, 2)).toEqual({ width: 1000, height: 600, dpi: 96, density: 1 })
    expect(displaySize(1000, 600, 2, true)).toEqual({
      width: 2000,
      height: 1200,
      dpi: 192,
      density: 2,
    })
    expect(displaySize(400, 800, 3, true)).toEqual({
      width: 800,
      height: 1600,
      dpi: 192,
      density: 2,
    })
  })
})

describe("pathLabel", () => {
  test("says direct or which relay, and nothing when the tailnet does not know", () => {
    expect(pathLabel({ direct: true, relay: "" })).toBe("direct")
    expect(pathLabel({ direct: false, relay: "fra" })).toBe("relayed via fra")
    expect(pathLabel({ direct: false, relay: "" })).toBeUndefined()
    expect(pathLabel(undefined)).toBeUndefined()
  })
})

describe("defaultSessionMode", () => {
  test("opens a Mac paired with Sunshine in Stream and everything else in Control", () => {
    expect(defaultSessionMode({ os: "macos", streamHostId: 41 })).toBe("stream")
    expect(defaultSessionMode({ os: "macos", streamHostId: 0 })).toBe("control")
    expect(defaultSessionMode({ os: "windows", streamHostId: 41 })).toBe("control")
  })
})

describe("combosFor", () => {
  test("offers Ctrl+Alt+Del to Windows and not to a Mac", () => {
    expect(combosFor("windows").map((c) => c.key)).toContain("cad")
    expect(combosFor("macos").map((c) => c.key)).not.toContain("cad")
  })
})

const stream = (over) => ({
  bridge: { state: "running" },
  sunshine: { state: "open", checkedAt: 0 },
  hostId: 0,
  paired: false,
  pairing: false,
  apps: [],
  ...over,
})

describe("streamStage", () => {
  test("waits on the bridge, then Sunshine, then the pairing, in that order", () => {
    expect(streamStage(stream({ bridge: { state: "missing" }, paired: true }))).toBe("bridge")
    expect(streamStage(stream({ sunshine: { state: "closed", checkedAt: 0 } }))).toBe("sunshine")
    expect(streamStage(stream({}))).toBe("pair")
    expect(streamStage(stream({ pairing: true, pin: "4821" }))).toBe("pin")
    expect(streamStage(stream({ paired: true, pairing: true }))).toBe("ready")
    // Paired, but Sunshine stopped since: it has to be started again.
    expect(streamStage(stream({ paired: true, sunshine: { state: "closed", checkedAt: 0 } }))).toBe(
      "sunshine",
    )
  })
})

describe("desktopApp", () => {
  test("opens Sunshine's Desktop, whatever order the apps come in", () => {
    const apps = [
      { id: 7, title: "Steam Big Picture" },
      { id: 3, title: "Desktop" },
    ]
    expect(desktopApp(apps)?.id).toBe(3)
    expect(desktopApp([{ id: 9, title: "Game" }])?.id).toBe(9)
    expect(desktopApp([])).toBeUndefined()
  })
})

describe("bridgeSettings", () => {
  test("keeps the reader's own settings and sets how the picture travels", () => {
    const next = bridgeSettings(
      { mouseMode: "relative", dataTransport: "webrtc" },
      "sharp",
      "websocket",
    )
    expect(next).toMatchObject({
      mouseMode: "relative",
      dataTransport: "websocket",
      videoCodec: "h264",
      videoSize: "1440p",
      fps: 60,
      bitrate: 40000,
      videoFrameQueueSize: 3,
    })
  })

  test("asks for WebRTC only where the backend says this browser can reach it, with a fallback", () => {
    expect(bridgeSettings({}, "max", "webrtc")).toMatchObject({
      dataTransport: "auto",
      videoSize: "1440p",
      fps: 120,
      bitrate: 80000,
    })
  })

  test("asks for the same 16:9 size whatever the screen, over a size an older page asked for", () => {
    expect(
      bridgeSettings(
        { videoSize: "custom", videoSizeCustom: { width: 1662, height: 1080 } },
        "smooth",
        "websocket",
      ),
    ).toMatchObject({ videoSize: "1080p" })
  })
})

describe("streamOverscan", () => {
  test("draws a narrower screen's picture wider than its frame, by its side padding", () => {
    // A 16:10 Mac fitted into 16:9 is 90% of the picture's width.
    const overscan = streamOverscan(1.6)
    expect(overscan.width).toBeCloseTo(16 / 9 / 1.6, 10)
    expect(overscan.height).toBe(1)
    expect(1 / overscan.width).toBeCloseTo(0.9, 10)
  })

  test("draws a wider screen's picture taller than its frame, by its top and bottom padding", () => {
    const overscan = streamOverscan(21 / 9)
    expect(overscan.width).toBe(1)
    expect(overscan.height).toBeCloseTo(21 / 9 / (16 / 9), 10)
  })

  test("leaves a 16:9 screen's picture the size of its frame", () => {
    expect(streamOverscan(16 / 9)).toEqual({ width: 1, height: 1 })
  })
})

/** A frame `width` by `height` lit in `rect` and black elsewhere, as Sunshine pads one. */
function frame(width, height, rect = { x: 0, y: 0, width, height }, level = 200) {
  const pixels = new Uint8ClampedArray(width * height * 4)
  for (let y = rect.y; y < rect.y + rect.height; y++)
    for (let x = rect.x; x < rect.x + rect.width; x++) {
      const at = (y * width + x) * 4
      pixels[at] = level
      pixels[at + 1] = level
      pixels[at + 2] = level
      pixels[at + 3] = 255
    }
  return pixels
}

describe("screenShape", () => {
  test("a frame lit to every edge is the screen's own shape", () => {
    expect(screenShape(frame(160, 90), 160, 90, 16 / 9)).toBe(16 / 9)
  })

  test("padding down both sides gives away a narrower screen", () => {
    // A 16:10 Mac fitted into 16:9 is 144 of 160 columns wide.
    const shape = screenShape(frame(160, 90, { x: 8, y: 0, width: 144, height: 90 }), 160, 90)
    expect(shape).toBeCloseTo(1.6, 5)
  })

  test("padding above and below gives away a wider screen", () => {
    // 21:9 fitted into 16:9 is 69 of 90 rows tall, give or take a row.
    const shape = screenShape(frame(160, 90, { x: 0, y: 10, width: 160, height: 70 }), 160, 90)
    expect(shape).toBeCloseTo(160 / 70, 5)
  })

  test("reads against the picture's own aspect, not the sample's rounding", () => {
    const shape = screenShape(frame(160, 90, { x: 8, y: 0, width: 144, height: 90 }), 160, 90, 1.8)
    expect(shape).toBeCloseTo(1.8 * (144 / 160), 5)
  })

  test("dark at one edge, on both axes, or everywhere is the picture, not padding", () => {
    expect(screenShape(frame(160, 90, { x: 20, y: 0, width: 140, height: 90 }), 160, 90)).toBe(
      undefined,
    )
    expect(screenShape(frame(160, 90, { x: 20, y: 10, width: 120, height: 70 }), 160, 90)).toBe(
      undefined,
    )
    expect(screenShape(frame(160, 90, undefined, 0), 160, 90)).toBe(undefined)
  })

  test("a dark-grey picture is still lit; only near-black is padding", () => {
    expect(screenShape(frame(160, 90, undefined, 30), 160, 90)).toBe(160 / 90)
    expect(screenShape(frame(160, 90, undefined, 8), 160, 90)).toBe(undefined)
  })
})

describe("stream addresses", () => {
  test("frames the bridge's page through the dashboard, and reads the mode from the URL", () => {
    expect(streamPageUrl(41, 3)).toBe("/api/v1/remote-desktop/bridge/stream.html?hostId=41&appId=3")
    expect(sessionMode("stream")).toBe("stream")
    expect(sessionMode(null)).toBe("control")
    expect(sessionMode("anything")).toBe("control")
  })
})

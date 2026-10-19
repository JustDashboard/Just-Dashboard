import { expect, test } from "@playwright/test"
import {
  BRIDGE,
  ENGINE,
  instruction,
  mockRemoteDesktop,
  overview,
  OFFICE,
  MAC,
  PAIRED,
  streamView,
} from "./remote-desktop-fixture"

test("the overview draws the computers as the picture and cards, without tiles or a band", async ({
  page,
}) => {
  await mockRemoteDesktop(page)
  await page.goto("/remote-desktop")

  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity).toContainText("Remote Desktop")
  await expect(identity).toContainText("4 computers")
  await expect(identity).toContainText("2 ready now")
  await expect(identity).toContainText("1 session open")
  await expect(identity).toContainText("over Tailscale")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  const picture = page.locator("[data-slot=computer-picture]")
  await expect(picture.getByText("This browser")).toBeVisible()
  await expect(picture.getByRole("link", { name: "office-pc" })).toHaveAttribute(
    "href",
    `/remote-desktop/${OFFICE}`,
  )
  await expect(picture).toContainText("Screen Sharing not answering")
  await expect(picture).toContainText("guacd 1.6.0 · Moonlight Web 2.10.0")

  await expect(page.getByRole("region", { name: "Time on each" })).toHaveCount(0)

  const cards = page.locator("[data-card=computer]")
  await expect(cards).toHaveCount(4)
  await expect(cards.filter({ hasText: "studio-mac" })).toContainText("macOS · Screen Sharing")
  await expect(cards.filter({ hasText: "office-pc" })).toContainText("In a session")
  // Paired with Sunshine, the Mac offers Stream beside Connect; the others do not.
  await expect(page.getByRole("link", { name: "Stream studio-mac with Sunshine" })).toHaveAttribute(
    "href",
    `/remote-desktop/${MAC}?mode=stream`,
  )
  await expect(page.getByRole("link", { name: /^Stream .* with Sunshine$/ })).toHaveCount(1)

  // A desktop on the tailnet that is not added yet is one press from the flow;
  // the phone on the same tailnet is not offered.
  const tailnet = page.getByRole("list", { name: "Desktops on the tailnet" })
  await expect(tailnet.getByRole("link", { name: "Add workshop-pc" })).toHaveAttribute(
    "href",
    "/remote-desktop/new?peer=n06",
  )
  await expect(tailnet).not.toContainText("phone")
})

test("a server without the engine opens on the install, which runs as a job", async ({ page }) => {
  const { calls } = await mockRemoteDesktop(page, {
    overview: overview({
      engine: { ...ENGINE, state: "missing", pulled: false, address: undefined },
      computers: [],
      sessions: [],
    }),
  })
  await page.goto("/remote-desktop")
  await expect(page.getByText("Control your computers from here")).toBeVisible()
  await page.getByRole("button", { name: "Install Remote Desktop" }).click()
  // The job's console is up only until the job succeeds and the overview
  // reads the engine running, which on a loaded machine can be one frame.
  await expect.poll(() => calls.installs).toBe(1)
  await expect(page.getByText("Control your computers from here")).toBeHidden()
})

test("adding a tailnet PC says where Remote Desktop is, waits for the port and saves the sign-in", async ({
  page,
}) => {
  let open = false
  const { calls } = await mockRemoteDesktop(page, { probeOpen: () => open })
  await page.goto("/remote-desktop/new")
  await expect(
    page.getByRole("heading", { level: 1, name: "Which computer do you want to reach?" }),
  ).toBeVisible()
  await page.getByRole("button", { name: "Add workshop-pc" }).click()

  await expect(
    page.getByRole("heading", { level: 1, name: "Turn on Remote Desktop on workshop-pc" }),
  ).toBeVisible()
  await expect(page.getByText("Settings › System › Remote Desktop")).toBeVisible()
  await expect(
    page.getByRole("status").filter({ hasText: "Waiting for Remote Desktop" }),
  ).toContainText("workshop-pc:3389")
  open = true
  await expect(page.getByRole("status").filter({ hasText: "answers on port" })).toContainText(
    "41 ms",
    { timeout: 10_000 },
  )

  // Windows Home is reached over VNC, on its own port.
  await page.getByRole("radio", { name: "Home" }).click()
  await expect(page.getByText("Install TightVNC")).toBeVisible()
  await page.getByRole("radio", { name: "Pro or Enterprise" }).click()
  await page.getByRole("button", { name: "Continue" }).click()

  await page.getByLabel("Username").fill("wayy@outlook.com")
  await page.getByLabel("Password").fill("correct horse")
  await page.getByRole("button", { name: "Save and connect" }).click()
  await expect(page).toHaveURL(/\/remote-desktop\/0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e$/)
  expect(calls.added).toEqual([
    {
      name: "workshop-pc",
      os: "windows",
      host: "workshop-pc",
      protocol: "rdp",
      port: 3389,
      username: "wayy@outlook.com",
      domain: "",
      password: "correct horse",
      security: "",
      layout: "",
      peerId: "n06",
      gfx: false,
    },
  ])
  expect(calls.probes).toContain("workshop-pc:3389")
})

test("a session draws the screen the tunnel sends and offers the keys a browser keeps", async ({
  page,
}) => {
  await mockRemoteDesktop(page)
  let opened = ""
  await page.routeWebSocket(
    /\/api\/v1\/remote-desktop\/computers\/[0-9a-f]+\/session/,
    (socket) => {
      opened = socket.url()
      socket.send(instruction("", "6b1e3b54-6f2c-4d55-a0a2-5c7cf1c4b8d9"))
      socket.send(
        instruction("size", "0", "1280", "720") +
          instruction("cfill", "14", "0", "40", "90", "160", "255") +
          instruction("rect", "0", "0", "0", "1280", "720") +
          instruction("sync", "1"),
      )
    },
  )
  await page.goto(`/remote-desktop/${OFFICE}`)
  await expect(page.getByRole("application", { name: "The screen of office-pc" })).toBeVisible()
  await expect(page.locator("[data-slot=remote-screen] canvas").first()).toBeAttached()
  await expect(page.getByRole("button", { name: "Keys" })).toBeVisible()
  await page.getByRole("button", { name: "Keys" }).click()
  await expect(page.getByRole("menuitem", { name: "Ctrl+Alt+Del" })).toBeVisible()
  await page.keyboard.press("Escape")
  expect(opened).toMatch(/width=\d+&height=\d+&dpi=\d+&timezone=/)

  // The rail moves between computers without leaving the session page.
  await expect(
    page.getByRole("navigation", { name: "Computers" }).getByRole("link", { name: /studio-mac/ }),
  ).toHaveAttribute("href", `/remote-desktop/${MAC}`)
})

test("a file the computer puts in the drive's Download folder arrives as a download", async ({
  page,
}) => {
  await mockRemoteDesktop(page)
  await page.routeWebSocket(
    /\/api\/v1\/remote-desktop\/computers\/[0-9a-f]+\/session/,
    (socket) => {
      socket.send(instruction("", "6b1e3b54-6f2c-4d55-a0a2-5c7cf1c4b8d9"))
      socket.send(instruction("size", "0", "1280", "720") + instruction("sync", "1"))
      // The client acknowledges the stream before guacd sends its data.
      socket.onMessage((message) => {
        if (String(message).startsWith("3.ack,1.7,")) {
          socket.send(instruction("blob", "7", btoa("invoice 4471")) + instruction("end", "7"))
        }
      })
      setTimeout(() => socket.send(instruction("file", "7", "text/plain", "invoice.txt")), 500)
    },
  )
  await page.goto(`/remote-desktop/${OFFICE}`)
  const download = await page.waitForEvent("download")
  expect(download.suggestedFilename()).toBe("invoice.txt")
  await expect(page.getByRole("complementary", { name: "Session" })).toContainText("invoice.txt")
})

test("a Mac that asks for its account is answered from the page, and the answer is saved", async ({
  page,
}) => {
  const { calls } = await mockRemoteDesktop(page)
  const sent: string[] = []
  await page.routeWebSocket(
    /\/api\/v1\/remote-desktop\/computers\/[0-9a-f]+\/session/,
    (socket) => {
      socket.onMessage((message) => sent.push(String(message)))
      socket.send(instruction("", "6b1e3b54-6f2c-4d55-a0a2-5c7cf1c4b8d9"))
      setTimeout(() => socket.send(instruction("required", "username", "password")), 200)
    },
  )
  await page.goto(`/remote-desktop/${MAC}?mode=control`)
  await expect(page.getByText("Sign in to studio-mac")).toBeVisible()
  await page.getByLabel("Mac account name").fill("zoro")
  await page.getByLabel("Mac login password").fill("login-password")
  await page.getByRole("button", { name: "Sign in", exact: true }).click()
  await expect(page.getByText("Connecting to studio-mac…")).toBeVisible()
  await expect
    .poll(() => sent.some((m) => m.includes("4.argv,") && m.includes("8.username")))
    .toBe(true)
  await expect.poll(() => sent.some((m) => m.includes(btoa("zoro")))).toBe(true)
  await expect.poll(() => calls.updated.map((u) => u.id)).toEqual([MAC])
  expect(calls.updated[0].body).toMatchObject({ username: "zoro", password: "login-password" })
})

test("the screen's size is sent only once it is up, never while the computer signs in", async ({
  page,
}) => {
  await mockRemoteDesktop(page)
  const sent: string[] = []
  let up: (() => void) | undefined
  await page.routeWebSocket(
    /\/api\/v1\/remote-desktop\/computers\/[0-9a-f]+\/session/,
    (socket) => {
      socket.onMessage((message) => sent.push(String(message)))
      socket.send(instruction("", "6b1e3b54-6f2c-4d55-a0a2-5c7cf1c4b8d9"))
      up = () => socket.send(instruction("size", "0", "1280", "720") + instruction("sync", "1"))
    },
  )
  await page.goto(`/remote-desktop/${MAC}?mode=control`)
  await expect(page.getByText("Connecting to studio-mac…")).toBeVisible()
  await page.setViewportSize({ width: 1100, height: 760 })
  await page.waitForTimeout(1200)
  expect(sent.filter((m) => m.startsWith("4.size,"))).toEqual([])
  up?.()
  await expect(page.getByRole("application", { name: "The screen of studio-mac" })).toBeVisible()
  await expect.poll(() => sent.filter((m) => m.startsWith("4.size,")).length).toBeGreaterThan(0)
})

test("a refused session says what to check on the computer, in its words", async ({ page }) => {
  await mockRemoteDesktop(page)
  await page.routeWebSocket(
    /\/api\/v1\/remote-desktop\/computers\/[0-9a-f]+\/session/,
    (socket) => {
      socket.send(
        instruction("", "6b1e3b54-6f2c-4d55-a0a2-5c7cf1c4b8d9") +
          instruction("error", "Aborted. See logs.", "519"),
      )
      socket.close()
    },
  )
  await page.goto(`/remote-desktop/${MAC}?mode=control`)
  await expect(page.getByText("studio-mac is not answering")).toBeVisible()
  await expect(page.getByText("Screen Sharing is turned on")).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect again" })).toBeVisible()
})

test("removing a computer asks first and leaves the computer itself alone", async ({ page }) => {
  const { calls } = await mockRemoteDesktop(page)
  await page.goto("/remote-desktop")
  await page.getByRole("button", { name: "Actions for studio-mac" }).click()
  await page.getByRole("menuitem", { name: "Remove" }).click()
  await expect(page.getByRole("dialog", { name: "Remove studio-mac" })).toContainText(
    "Nothing changes on the computer itself",
  )
  await page
    .getByRole("dialog", { name: "Remove studio-mac" })
    .getByRole("button", { name: "Remove" })
    .click()
  await expect.poll(() => calls.deleted).toEqual([MAC])
})

test("Stream pairs with Sunshine, keeps the PIN over Control while it is typed, then frames the stream", async ({
  page,
}) => {
  const { calls, state } = await mockRemoteDesktop(page)
  let sessions = 0
  await page.routeWebSocket(
    /\/api\/v1\/remote-desktop\/computers\/[0-9a-f]+\/session/,
    (socket) => {
      sessions += 1
      socket.send(instruction("", "6b1e3b54-6f2c-4d55-a0a2-5c7cf1c4b8d9"))
    },
  )
  await page.goto(`/remote-desktop/${MAC}?mode=stream`)
  await expect(page.getByText("Pair with Sunshine on studio-mac")).toBeVisible()
  // Stream mode closes Control: no Guacamole session is opened under it.
  expect(sessions).toBe(0)

  await page.getByRole("button", { name: "Pair", exact: true }).click()
  await expect(page.getByLabel("PIN 4 8 2 1")).toBeVisible()
  expect(calls.pairs).toEqual([MAC])
  await expect(page.getByText("Type this PIN into Sunshine on studio-mac")).toBeVisible()

  await page.getByRole("button", { name: "Type it through Control" }).click()
  await expect(page).not.toHaveURL(/mode=stream/)
  await expect.poll(() => sessions).toBe(1)
  const card = page.getByRole("status").filter({ hasText: "Sunshine PIN" })
  await expect(card.getByLabel("PIN 4 8 2 1")).toBeVisible()

  // Sunshine takes the PIN: the card goes on its own, and Stream frames the bridge's page.
  state.stream = streamView(PAIRED)
  await expect(card).toBeHidden({ timeout: 15_000 })
  await page
    .getByRole("radiogroup", { name: "How the screen is drawn" })
    .getByRole("radio", { name: /Stream/ })
    .click()
  const frame = page.locator('iframe[data-slot="remote-stream"]')
  await expect(frame).toHaveAttribute(
    "src",
    "/api/v1/remote-desktop/bridge/stream.html?hostId=41&appId=3",
  )
  await expect(
    page.frameLocator('iframe[data-slot="remote-stream"]').getByText("The bridge's stream page"),
  ).toBeVisible()
  const settings = async () =>
    page.evaluate(() => JSON.parse(localStorage.getItem("mlSettings") ?? "{}"))
  expect(await settings()).toMatchObject({
    dataTransport: "websocket",
    videoCodec: "h264",
    videoSize: "custom",
    videoSizeCustom: { width: 1920, height: 1080 },
    fps: 60,
    bitrate: 20000,
    videoFrameQueueSize: 3,
  })

  await page.getByRole("radio", { name: "Sharp" }).click()
  await expect
    .poll(async () => (await settings()).videoSizeCustom)
    .toEqual({ width: 2560, height: 1440 })
  expect((await settings()).bitrate).toBe(40000)
})

test("a paired Mac opens in Stream unless the address asks for Control, which says why", async ({
  page,
}) => {
  await mockRemoteDesktop(page, { stream: streamView(PAIRED) })
  await page.goto(`/remote-desktop/${MAC}`)
  await expect(page).toHaveURL(/mode=stream/)
  await expect(page.locator('iframe[data-slot="remote-stream"]')).toBeVisible()

  // Asked for Control, a Mac gets it, with the reason Stream is the default
  // and one press back to it.
  await page.goto(`/remote-desktop/${MAC}?mode=control`)
  await expect(page).not.toHaveURL(/mode=stream/)
  const note = page.locator('[data-slot="mac-control-note"]')
  await expect(note).toContainText("Screen Sharing is slow")
  await note.getByRole("button", { name: "Switch to Stream" }).click()
  await expect(page).toHaveURL(/mode=stream/)

  // A PC opens in Control, whatever Sunshine is paired with.
  await page.goto(`/remote-desktop/${OFFICE}`)
  await expect(page).not.toHaveURL(/mode=stream/)
  await expect(page.locator('[data-slot="mac-control-note"]')).toHaveCount(0)
})

test("Stream installs its bridge as a job on a server that has none", async ({ page }) => {
  const { calls } = await mockRemoteDesktop(page, {
    stream: streamView({ bridge: { ...BRIDGE, state: "missing", address: undefined } }),
  })
  await page.goto(`/remote-desktop/${OFFICE}?mode=stream`)
  await page.getByRole("button", { name: "Install Stream mode" }).click()
  await expect(page.getByText("Pair with Sunshine on office-pc")).toBeVisible()
  expect(calls.streamInstalls).toBe(1)
})

test("Stream says where Sunshine is turned on, in the computer's own system's words", async ({
  page,
}) => {
  await mockRemoteDesktop(page, {
    stream: streamView({ sunshine: { state: "closed", error: "refused", checkedAt: 0 } }),
  })
  await page.goto(`/remote-desktop/${MAC}?mode=stream`)
  await expect(page.getByText("Turn on Sunshine on studio-mac")).toBeVisible()
  await expect(page.getByText("100.101.12.40:47989 (refused)", { exact: false })).toBeVisible()
  await expect(page.getByText("brew install lizardbyte/homebrew/sunshine")).toBeVisible()
  await page.getByRole("button", { name: "Do it through Control" }).click()
  await expect(page).not.toHaveURL(/mode=stream/)
})

test("a Sunshine that takes the connection and answers nothing is called stuck, with how to free it", async ({
  page,
}) => {
  await mockRemoteDesktop(page, {
    stream: streamView({ sunshine: { state: "closed", error: "stuck", checkedAt: 0 } }),
  })
  await page.goto(`/remote-desktop/${MAC}?mode=stream`)
  await expect(page.getByText("Sunshine on studio-mac is stuck")).toBeVisible()
  await expect(page.getByText("Sunshine stuck")).toBeVisible()
  await expect(page.getByText("brew services restart sunshine")).toBeVisible()
  await expect(page.getByText("Look for a prompt on the computer's screen")).toBeVisible()
  await expect(page.getByRole("button", { name: "Pair", exact: true })).toHaveCount(0)
})

test("a session stays connected while another page is up, and only Disconnect ends it", async ({
  page,
}) => {
  await mockRemoteDesktop(page)
  let opened = 0
  let closed = 0
  await page.routeWebSocket(
    /\/api\/v1\/remote-desktop\/computers\/[0-9a-f]+\/session/,
    (socket) => {
      opened += 1
      socket.onClose(() => (closed += 1))
      socket.send(instruction("", "6b1e3b54-6f2c-4d55-a0a2-5c7cf1c4b8d9"))
      socket.send(
        instruction("size", "0", "1280", "720") +
          instruction("rect", "0", "0", "0", "1280", "720") +
          instruction("sync", "1"),
      )
    },
  )
  await page.goto(`/remote-desktop/${OFFICE}`)
  const screen = page.getByRole("application", { name: "The screen of office-pc" })
  await expect(screen).toBeVisible()

  await page
    .getByRole("navigation", { name: "Computers" })
    .getByRole("link", { name: "Computers" })
    .click()
  await expect(page).toHaveURL(/\/remote-desktop$/)
  const dock = page.getByRole("complementary", { name: "Remote sessions" })
  await expect(dock).toContainText("office-pc")
  await expect(dock).toContainText("Remote Desktop · 43 ms")
  // The rail's Remote Desktop leads back into the session, not to the overview.
  await expect(page.getByRole("link", { name: "Remote Desktop", exact: true })).toHaveAttribute(
    "href",
    `/remote-desktop/${OFFICE}?mode=control`,
  )
  expect(closed).toBe(0)

  await dock.getByRole("link", { name: "Open office-pc" }).click()
  await expect(screen).toBeVisible()
  await expect(dock).toBeHidden()
  expect(opened).toBe(1)
  expect(closed).toBe(0)

  await page.getByRole("link", { name: "Disconnect" }).click()
  await expect(page).toHaveURL(/\/remote-desktop$/)
  await expect.poll(() => closed).toBe(1)
  await expect(page.getByRole("complementary", { name: "Remote sessions" })).toHaveCount(0)
})

test("a stream keeps playing while another page is up, with only the computer's own pointer", async ({
  page,
}) => {
  await mockRemoteDesktop(page, { stream: streamView(PAIRED) })
  let loads = 0
  page.on("request", (request) => {
    if (request.url().includes("/bridge/stream.html")) loads += 1
  })
  await page.goto(`/remote-desktop/${MAC}?mode=stream`)
  const frame = page.locator('iframe[data-slot="remote-stream"]')
  await expect(frame).toBeVisible()
  const hidden = () =>
    frame.evaluate((node) =>
      Boolean((node as HTMLIFrameElement).contentDocument?.getElementById("jd-hide-pointer")),
    )
  await expect.poll(hidden).toBe(true)
  await page.getByRole("switch", { name: "Show this computer's pointer too" }).click()
  await expect.poll(hidden).toBe(false)
  await page
    .frameLocator('iframe[data-slot="remote-stream"]')
    .locator("body")
    .evaluate((body) => {
      const video = body.ownerDocument.createElement("video")
      Object.defineProperties(video, {
        paused: { value: false },
        ended: { value: false },
        readyState: { value: 3 },
        videoWidth: { value: 1920 },
      })
      body.append(video)
      video.dispatchEvent(new Event("playing"))
    })
  await expect(page.getByText("streaming", { exact: true })).toBeVisible()

  await page
    .getByRole("navigation", { name: "Computers" })
    .getByRole("link", { name: "Computers" })
    .click()
  const dock = page.getByRole("complementary", { name: "Remote sessions" })
  await expect(dock).toContainText("Streaming")
  await dock.getByRole("link", { name: "Open studio-mac" }).click()
  await expect(frame).toBeVisible()
  await expect(page).toHaveURL(/mode=stream/)
  expect(loads).toBe(1)
})

test("the session's column switches how the screen is drawn, and the computers can be put away", async ({
  page,
}) => {
  await mockRemoteDesktop(page, { stream: streamView(PAIRED) })
  await page.routeWebSocket(
    /\/api\/v1\/remote-desktop\/computers\/[0-9a-f]+\/session/,
    (socket) => {
      socket.send(instruction("", "6b1e3b54-6f2c-4d55-a0a2-5c7cf1c4b8d9"))
    },
  )
  await page.goto(`/remote-desktop/${MAC}?mode=control`)
  const panel = page.getByRole("complementary", { name: "Session" })
  const modes = panel.getByRole("radiogroup", { name: "How the screen is drawn" })
  await expect(modes.getByRole("radio", { name: /Screen Sharing/ })).toHaveAttribute(
    "aria-checked",
    "true",
  )
  // The top bar names the computer and how it is drawn, with no switch in it.
  await expect(page.locator("[data-slot=pane-header]").getByRole("radio")).toHaveCount(0)

  await modes.getByRole("radio", { name: /Stream/ }).click()
  await expect(page).toHaveURL(/mode=stream/)
  await expect(page.locator('iframe[data-slot="remote-stream"]')).toBeVisible()
  await expect(panel.getByRole("radio", { name: "Smooth" })).toHaveAttribute("aria-checked", "true")
  await expect(panel).toContainText("Paired with Sunshine")

  const computers = page.getByRole("navigation", { name: "Computers" })
  await expect(computers).toBeVisible()
  await page.getByRole("button", { name: "Hide the computers" }).click()
  await expect(computers).toBeHidden()
  await page.getByRole("button", { name: "Show the computers" }).click()
  await expect(computers).toBeVisible()
})

test("a browser on the tailnet is sent the stream over WebRTC, at up to Max", async ({ page }) => {
  await mockRemoteDesktop(page, {
    stream: streamView({
      ...PAIRED,
      transport: "webrtc",
      bridge: { ...BRIDGE, media: "100.110.34.31:40000-40019" },
    }),
  })
  await page.goto(`/remote-desktop/${MAC}?mode=stream`)
  await expect(page.locator('iframe[data-slot="remote-stream"]')).toBeVisible()
  const panel = page.getByRole("complementary", { name: "Session" })
  await expect(panel.getByText("WebRTC", { exact: true })).toBeVisible()
  const settings = async () =>
    page.evaluate(() => JSON.parse(localStorage.getItem("mlSettings") ?? "{}"))
  await expect.poll(async () => (await settings()).dataTransport).toBe("auto")

  await panel.getByRole("radio", { name: "Max" }).click()
  await expect.poll(async () => (await settings()).fps).toBe(120)
  expect(await settings()).toMatchObject({
    videoSize: "custom",
    videoSizeCustom: { width: 2560, height: 1440 },
    bitrate: 80000,
    videoFrameQueueSize: 3,
  })
})

/**
 * Sunshine's part, played by the bridge's page: a 16:10 screen fitted into
 * the size the stream asks for, padded black, drawn the way the bridge draws
 * its picture — fixed in the middle of its page, contained.
 */
const SIXTEEN_TEN_BRIDGE = `<!doctype html><title>Stream</title>
<style>
  html, body { margin: 0; height: 100%; background: #000; }
  .video-stream {
    position: fixed; top: 50%; left: 50%; transform: translate(-50%, -50%);
    width: 100%; height: 100%; object-fit: contain;
  }
</style>
<div id="input" tabindex="0"></div><div id="root"></div>
<script>
  const settings = JSON.parse(localStorage.getItem("mlSettings") || "{}")
  const { width, height } = settings.videoSizeCustom
  const canvas = document.createElement("canvas")
  canvas.className = "video-stream"
  canvas.width = width
  canvas.height = height
  const context = canvas.getContext("2d")
  context.fillStyle = "#000"
  context.fillRect(0, 0, width, height)
  const screen = Math.min(width / 1.6, height)
  context.fillStyle = "#3a6ea5"
  context.fillRect((width - screen * 1.6) / 2, (height - screen) / 2, screen * 1.6, screen)
  document.getElementById("root").append(canvas)
</script>`

test("a stream takes the whole content area, in the computer's own shape, with nothing letterboxed", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1600, height: 900 })
  await mockRemoteDesktop(page, { stream: streamView(PAIRED) })
  await page.route("**/api/v1/remote-desktop/bridge/**", (route) =>
    route.fulfill({ status: 200, contentType: "text/html", body: SIXTEEN_TEN_BRIDGE }),
  )
  await page.goto(`/remote-desktop/${MAC}?mode=stream`)
  await page.getByRole("button", { name: "Hide the computers" }).click()
  await page.getByRole("button", { name: "Hide the session tools" }).click()

  // The session is the shell's content area, edge to edge: no measure, no gutter.
  const workspace = await page.locator("[data-slot=remote-workspace]").boundingBox()
  const area = await page.locator("[data-workspace-shell-scroll]").boundingBox()
  expect(workspace).toEqual(area)

  // Sunshine padded the Mac into 16:9; the stream is asked for again at 16:10.
  const settings = async () =>
    page.evaluate(() => JSON.parse(localStorage.getItem("mlSettings") ?? "{}").videoSizeCustom)
  await expect.poll(settings).toEqual({ width: 1920, height: 1080 })
  await expect.poll(settings, { timeout: 10_000 }).toEqual({ width: 1728, height: 1080 })

  // The frame is the stream's own shape, as large as the space under the strip.
  const frame = page.locator('iframe[data-slot="remote-stream"]')
  const ground = frame.locator("xpath=..")
  await expect
    .poll(async () => {
      const box = await frame.boundingBox()
      return box ? Math.round((box.width / box.height) * 100) / 100 : 0
    })
    .toBe(1.6)
  const [frameBox, groundBox] = [await frame.boundingBox(), await ground.boundingBox()]
  expect(
    Math.min(
      Math.abs(frameBox!.width - groundBox!.width),
      Math.abs(frameBox!.height - groundBox!.height),
    ),
  ).toBeLessThanOrEqual(1)

  // The computer's shape is remembered: the next session asks for it at once.
  await page.reload()
  await expect(frame).toBeVisible()
  await expect.poll(settings, { timeout: 1500 }).toEqual({ width: 1728, height: 1080 })
})

for (const width of [390, 1024]) {
  test(`session tools remain accessible at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 })
    await mockRemoteDesktop(page, { stream: streamView(PAIRED) })
    await page.goto(`/remote-desktop/${MAC}?mode=stream`)
    await page.getByRole("button", { name: "Show the session tools" }).click()
    const tools = page.getByRole("dialog", { name: "Session tools" })
    await expect(tools.getByRole("radiogroup", { name: "How the screen is drawn" })).toBeVisible()
    await expect(tools.getByRole("radiogroup", { name: "Stream quality" })).toBeVisible()
    await expect(
      tools.getByRole("switch", { name: "Show this computer's pointer too" }),
    ).toBeVisible()
    await tools.getByRole("radio", { name: /Screen Sharing/ }).click()
    await page.getByRole("button", { name: "Show the session tools" }).click()
    await expect(tools.getByLabel("Text to paste on studio-mac")).toBeVisible()
    await tools.getByRole("button", { name: "Close", exact: true }).click()
    const trigger = page.getByRole("button", { name: "Show the session tools" })
    await expect(trigger).toBeFocused()
    await trigger.press("Enter")
    await expect(tools).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(trigger).toBeFocused()
  })
}

for (const path of ["/remote-desktop/new", `/remote-desktop/${MAC}?mode=control`]) {
  test(`inventory failures show their reason and retry on ${path}`, async ({ page }) => {
    await mockRemoteDesktop(page)
    let fail = true
    await page.route("**/api/v1/remote-desktop/", async (route) => {
      if (!fail) return route.fallback()
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "unavailable",
            message: "Computer inventory unavailable",
            retryable: false,
          },
        }),
      })
    })
    await page.goto(path)
    await expect(
      page.getByRole("alert").filter({ hasText: "Computer inventory unavailable" }),
    ).toContainText("Computer inventory unavailable")
    fail = false
    await page.getByRole("button", { name: "Try again" }).click()
    await expect(
      page.getByRole("alert").filter({ hasText: "Computer inventory unavailable" }),
    ).toHaveCount(0)
    if (path.endsWith("/new"))
      await expect(page.getByRole("button", { name: "Add workshop-pc" })).toBeVisible()
    else await expect(page.getByText("Connecting to studio-mac…")).toBeVisible()
  })
}

test("a paired Sunshine without applications is ready but has no live session", async ({
  page,
}) => {
  await mockRemoteDesktop(page, { stream: streamView({ ...PAIRED, apps: [] }) })
  await page.goto(`/remote-desktop/${MAC}?mode=stream`)
  await expect(page.getByText("ready to stream", { exact: true })).toBeVisible()
  await expect(page.getByText(/offers nothing to stream/)).toBeVisible()
  await expect(page.locator('iframe[data-slot="remote-stream"]')).toHaveCount(0)
  await expect(page.getByText("streaming", { exact: true })).toHaveCount(0)
})

test("streaming status requires playback and returns to ready when video stalls", async ({
  page,
}) => {
  await mockRemoteDesktop(page, { stream: streamView(PAIRED) })
  await page.goto(`/remote-desktop/${MAC}?mode=stream`)
  const frame = page.frameLocator('iframe[data-slot="remote-stream"]')
  await expect(frame.getByText("The bridge's stream page")).toBeVisible()
  await expect(page.getByText("ready to stream", { exact: true })).toBeVisible()
  await frame.locator("body").evaluate((body) => {
    const video = body.ownerDocument.createElement("video")
    Object.defineProperties(video, {
      paused: { value: false },
      ended: { value: false },
      readyState: { value: 3 },
      videoWidth: { value: 1920 },
    })
    body.append(video)
    video.dispatchEvent(new Event("playing"))
  })
  await expect(page.getByText("streaming", { exact: true })).toBeVisible()
  await frame.locator("video").evaluate((video) => video.dispatchEvent(new Event("waiting")))
  await expect(page.getByText("ready to stream", { exact: true })).toBeVisible()
  await frame.locator("video").evaluate((video) => video.dispatchEvent(new Event("timeupdate")))
  await expect(page.getByText("ready to stream", { exact: true })).toBeVisible()
  await frame.locator("video").evaluate((video) => video.dispatchEvent(new Event("playing")))
  await expect(page.getByText("streaming", { exact: true })).toBeVisible()
  // A new inaccessible document supplies no playback proof from the former player.
  await page.locator('iframe[data-slot="remote-stream"]').evaluate((node) => {
    const iframe = node as HTMLIFrameElement
    iframe.setAttribute("sandbox", "")
    iframe.src += "&isolated=true"
  })
  await expect(page.getByText("ready to stream", { exact: true })).toBeVisible()
})

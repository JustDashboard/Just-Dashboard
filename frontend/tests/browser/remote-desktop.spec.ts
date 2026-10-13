import { expect, test } from "@playwright/test"
import {
  ENGINE,
  instruction,
  mockRemoteDesktop,
  overview,
  OFFICE,
  MAC,
} from "./remote-desktop-fixture"

test("the overview draws the computers as the picture, the band and cards, without tiles", async ({
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

  const band = page.locator("[data-slot=remote-band]")
  await expect(band.getByRole("region", { name: "Reach" })).toContainText("of 4 ready")
  await expect(band.getByRole("region", { name: "Time on each" })).toContainText("in 14 days")
  await expect(band.getByRole("region", { name: "Recent sessions" })).toContainText("now")

  const cards = page.locator("[data-card=computer]")
  await expect(cards).toHaveCount(4)
  await expect(page.locator("[data-slot=computer-hue]")).toHaveCount(4)
  await expect(cards.filter({ hasText: "studio-mac" })).toContainText("macOS · Screen Sharing")
  await expect(cards.filter({ hasText: "office-pc" })).toContainText("In a session")

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
  await expect(page.getByText("guacd 1.6.0 is ready")).toBeVisible()
  expect(calls.installs).toBe(1)
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
  await expect(page.getByRole("complementary", { name: "Session tools" })).toContainText(
    "invoice.txt",
  )
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
  await page.goto(`/remote-desktop/${MAC}`)
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

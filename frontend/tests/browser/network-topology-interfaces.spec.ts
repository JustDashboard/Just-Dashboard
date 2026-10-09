import { expect, test, type Page } from "@playwright/test"
import {
  iso,
  json,
  links,
  loaded,
  mockNetwork,
  namespaces,
  overview,
  type Mutation,
} from "./network-fixture"

/**
 * The Overview's identity, flow paths, throughput age and incident history,
 * and the Interfaces page's device, bridge, readiness and namespace readings,
 * against the mocked API. Each check is about what a page does with an
 * answer — a failed reading drawn as failed, a selection drilled into the
 * devices under it, a membership change previewed before it is offered.
 */

test.use({ viewport: { width: 1440, height: 1000 } })

const now = () => Math.floor(Date.now() / 1000)

async function openDevice(page: Page, name: string) {
  await page.goto(`/network/interfaces?device=${encodeURIComponent(name)}`)
  await loaded(page)
  return page.getByRole("dialog")
}

test("the identity says both forwarding families and keeps a translated source from posing as public", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/overview": {
        ...overview,
        forwarding: { ipv4: true, ipv6: false },
        identity: [
          {
            ...overview.identity[0],
            source: "10.0.0.5",
            sourceScope: "private",
            public: "translated",
            detail:
              "Traffic leaves from 10.0.0.5, which is not routable on the internet, so a router or the provider translates it. The public address it becomes was not observed by this host.",
          },
          { ...overview.identity[1], forwarding: false },
        ],
      },
    },
  })
  await page.goto("/network")
  await loaded(page)
  await expect(page.getByTestId("forwarding-families")).toHaveText(
    "IPv4 routing · IPv6 not routing",
  )
  await expect(
    page.locator("[data-slot=host-identity]").getByText(
      "translated upstream; public address not observed",
    ),
  ).toBeVisible()
  const ways = page.getByRole("table").filter({ hasText: "The internet sees" })
  const v4 = ways.locator("tr[data-family=inet]")
  await expect(v4).toContainText("10.0.0.5 · private")
  await expect(v4).toContainText("was not observed by this host")
  await expect(ways.locator("tr[data-family=inet6]")).toContainText("NIC address is public")
  await expect(ways.locator("tr[data-family=inet6]")).toContainText("off")
})

test("paths in use come from tracked flows and pointing at a node lights who it trades with", async ({
  page,
}) => {
  await mockNetwork(page)
  await page.goto("/network")
  await loaded(page)
  const paths = page.getByRole("list", { name: "Paths in use" })
  await expect(paths.getByText("31 flows")).toBeVisible()
  await expect(paths.getByText("masqueraded")).toBeVisible()
  await expect(
    paths.getByText("web → br-93e5e9c9442b → this server → masquerade on ens3 → the internet"),
  ).toBeVisible()
  await expect(page.getByText("57 tracked flows")).toBeVisible()
  await expect(paths.getByRole("link", { name: "The internet" }).first()).toHaveAttribute(
    "href",
    "/network/interfaces?device=ens3",
  )
  await page.locator('li[data-node="internet"]').hover()
  await expect(page.locator('li[data-node="docker:93e5e9c9442b5f1a"]')).not.toHaveAttribute(
    "data-dim",
    "",
  )
  await expect(page.locator('li[data-node="docker:b17d23d019f2aa00"]')).toHaveAttribute(
    "data-dim",
    "",
  )
  await expect(page.locator('li[data-node="internet"]')).toContainText("49 tracked flows")
})

test("a failed connection-tracking read is said, not drawn as no traffic", async ({ page }) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/overview": {
        ...overview,
        flows: {
          state: "failed",
          error: "reading connection tracking needs CAP_NET_ADMIN in the host's network namespace",
          readAt: iso(0),
          total: 0,
          classified: 0,
          truncated: false,
          accounting: false,
          edges: [],
        },
      },
    },
  })
  await page.goto("/network")
  await loaded(page)
  await expect(page.getByText("Connection tracking could not be read")).toBeVisible()
  await expect(page.getByText("CAP_NET_ADMIN", { exact: false })).toBeVisible()
  await expect(page.getByText("No tracked flow crosses")).toHaveCount(0)
})

test("throughput says how old its newest reading is and turns stale when the ring stops", async ({
  page,
}) => {
  await mockNetwork(page)
  await page.goto("/network")
  await loaded(page)
  await expect(page.getByTestId("observation-age")).toHaveText(/observed \d+ s ago/)

  const stale = now() - 120
  await mockNetwork(page, [], {
    overrides: {
      "/network/traffic/live": {
        now: stale,
        series: { ens3: [{ t: stale - 2, rx: 10, tx: 20 }, { t: stale, rx: 10, tx: 20 }] },
      },
    },
  })
  await page.goto("/network")
  await loaded(page)
  await expect(page.getByText(/Last reading 2 min ago/)).toBeVisible()
})

test("a failed live poll keeps the ring with a dated retry", async ({ page }) => {
  await mockNetwork(page)
  let reads = 0
  await page.route("**/api/v1/network/traffic/live*", async (route) => {
    reads++
    if (reads === 1) {
      const t = now()
      return json(route, { now: t, series: { ens3: [{ t: t - 2, rx: 100, tx: 50 }, { t, rx: 120, tx: 60 }] } })
    }
    return json(route, { error: { code: "busy", message: "The sampler did not answer" } }, 503)
  })
  await page.goto("/network")
  await loaded(page)
  const warning = page.getByRole("alert").filter({ hasText: "Showing the last known live throughput" })
  await expect(warning).toBeVisible()
  await expect(warning.getByText("The sampler did not answer")).toBeVisible()
  await expect(warning.locator("time")).toHaveAttribute("datetime", /T/)
  await expect(warning.getByRole("button", { name: "Refresh" })).toBeVisible()
})

test("the throughput tiles and the chart drill into the devices under them", async ({ page }) => {
  await mockNetwork(page)
  await page.goto("/network")
  await loaded(page)
  const chart = page.locator("[data-slot=panel]").filter({ hasText: "Last 15 minutes" }).first()
  await expect(chart.getByRole("link", { name: "wg0" })).toHaveAttribute(
    "href",
    "/network/interfaces?device=wg0",
  )
  const plot = chart.locator(".recharts-wrapper").first()
  await plot.scrollIntoViewIfNeeded()
  const box = await plot.boundingBox()
  if (!box) throw new Error("the chart has no box")
  await page.mouse.move(box.x + box.width * 0.3, box.y + box.height / 2)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width * 0.5, box.y + box.height / 2, { steps: 8 })
  await page.mouse.move(box.x + box.width * 0.7, box.y + box.height / 2, { steps: 8 })
  await page.mouse.up()
  const carried = page.getByLabel("What carried the selection")
  await expect(carried.getByText(/Carried between/)).toBeVisible()
  await expect(carried.getByRole("link", { name: "ens3" })).toBeVisible()
  await carried.getByRole("button", { name: "Clear" }).click()
  await expect(carried).toHaveCount(0)

  await page.getByRole("link", { name: /Open ens3: what it received/ }).click()
  await expect(page).toHaveURL(/\/network\/interfaces/)
  await expect(page.getByRole("dialog").getByText("ens3").first()).toBeVisible()
})

test("a failed reading is its own finding, and history keeps unobserved incidents open", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/overview": {
        ...overview,
        firewall: { available: false, enabled: false, rules: 0 },
        observations: [
          {
            source: "firewall",
            label: "The host firewall",
            state: "failed",
            error: "ufw status timed out",
            href: "/network/firewall",
          },
        ],
        findings: [
          {
            id: "observation.firewall",
            level: "warning",
            source: "firewall",
            title: "The host firewall could not be read",
            detail:
              "ufw status timed out What depends on it is not judged until it reads again; this is not evidence that nothing is wrong.",
            href: "/network/firewall",
          },
        ],
        incidents: [
          {
            id: 9,
            findingId: "firewall.off",
            source: "firewall",
            level: "warning",
            title: "The firewall is installed but not enforcing",
            detail: "Its rules exist and are not applied.",
            href: "/network/firewall",
            openedAt: iso(30),
            lastSeenAt: iso(5),
            unobservedSince: iso(4),
            related: [],
          },
          ...overview.incidents,
        ],
      },
    },
  })
  await page.goto("/network")
  await loaded(page)
  await expect(page.getByText("The host firewall could not be read")).toBeVisible()
  await expect(page.getByText("No supported host firewall manager was detected")).toHaveCount(0)
  const history = page.getByRole("list", { name: "Incident history" })
  await expect(history.getByText("Unobserved")).toBeVisible()
  await expect(history.getByText(/so whether it is still true is unknown/)).toBeVisible()
  await expect(history.getByText("Resolved")).toBeVisible()
  await expect(history.getByText(/began with ens3 has no carrier/)).toBeVisible()
})

test("interfaces flag Docker devices that could not be joined instead of hiding them", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/links": links.map((l) =>
        l.name === "veth6e4f828"
          ? {
              ...l,
              container: undefined,
              containerImage: undefined,
              dockerJoin: "unresolved",
              dockerJoinReason:
                "Docker's container list could not be read (Cannot connect to the Docker daemon), so the container behind this device is unknown.",
            }
          : l,
      ),
    },
  })
  await page.goto("/network/interfaces")
  await loaded(page)
  await expect(page.getByText("1 Docker device not joined to a container or network")).toBeVisible()
  await expect(page.getByText(/Cannot connect to the Docker daemon/).first()).toBeVisible()
  await page.getByRole("radio", { name: /Everything/ }).or(page.getByRole("button", { name: /Everything/ })).first().click()
  await expect(page.getByText("container not joined · on docker0")).toBeVisible()
})

test("a device sheet reads its driver, offloads, error counters and address origins", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/links": links.map((l) =>
        l.name === "ens3"
          ? {
              ...l,
              addresses: (l.addresses as Record<string, unknown>[]).map((a, i) =>
                i === 0 ? { ...a, dynamic: true, origin: "dhcp", validSeconds: 83020 } : a,
              ),
            }
          : l,
      ),
    },
  })
  const sheet = await openDevice(page, "ens3")
  const hardware = sheet.locator("[data-slot=panel]").filter({ hasText: "Driver and errors" })
  await expect(hardware.getByText("virtio_net 1.0.0")).toBeVisible()
  await expect(hardware.getByText("0000:00:03.0")).toBeVisible()
  await expect(hardware.getByRole("list", { name: "Offloads" })).toContainText("rx-checksumming · fixed")
  await expect(hardware.getByText("CRC errors (received)")).toBeVisible()
  await expect(hardware.getByText("41")).toBeVisible()
  await expect(sheet.getByText(/from DHCP, valid 23 h 3 min/)).toBeVisible()
})

test("namespaces keep a failed item visible and open as their own network", async ({ page }) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/namespaces": [
        ...namespaces,
        {
          name: "gone",
          kind: "container",
          managed: false,
          image: "busybox",
          pid: 99,
          devices: [],
          readError: "nsenter: cannot open /proc/99/ns/net: No such file or directory",
        },
      ],
    },
  })
  await page.goto("/network/interfaces")
  await loaded(page)
  await page.getByRole("switch", { name: "Show containers' namespaces" }).click()
  await expect(page.getByText(/Its devices could not be read: nsenter: cannot open/)).toBeVisible()
  await expect(page.getByText("devices unknown")).toBeVisible()

  await page.getByRole("button", { name: "Inspect lab" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByRole("table", { name: "Namespace routes" })).toContainText("default")
  await expect(sheet.getByText("192.168.50.1").first()).toBeVisible()
  await sheet.getByLabel("Route to").fill("1.1.1.1")
  await sheet.getByRole("button", { name: "Ask" }).click()
  await expect(sheet.getByLabel("Route answer")).toHaveText(
    "Leaves through lab-n via 192.168.50.1, from 192.168.50.20.",
  )
})

test("a later namespaces poll failure keeps the list with a dated retry", async ({ page }) => {
  let reads = 0
  await mockNetwork(page)
  await page.route("**/api/v1/network/namespaces", async (route) => {
    reads++
    return reads === 1
      ? json(route, namespaces)
      : json(route, { error: { code: "unavailable", message: "ip netns failed" } }, 503)
  })
  await page.clock.install()
  await page.goto("/network/interfaces")
  await loaded(page)
  await expect.poll(() => reads).toBe(1)
  await page.clock.fastForward(30_100)
  const warning = page.getByRole("alert").filter({ hasText: "Showing the last known namespaces" })
  await expect(warning).toBeVisible()
  await expect(warning.getByText("ip netns failed")).toBeVisible()
  await expect(page.getByRole("button", { name: "Inspect lab" })).toBeVisible()
})

test("a veth hands off to the namespace it leads into and a container's to the investigator", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/links": [
        ...links,
        {
          ...links.find((l) => l.name === "vlan30"),
          name: "lab-h",
          index: 40,
          kind: "veth",
          role: "virtual",
          master: "jd-lab",
          parent: undefined,
          vlanId: undefined,
          peer: "lab-n",
          peerNamespace: "lab",
        },
      ],
    },
  })
  const sheet = await openDevice(page, "lab-h")
  await expect(sheet.getByText("Its other end is lab-n inside lab.")).toBeVisible()
  await sheet.getByRole("button", { name: "Open namespace lab" }).click()
  await expect(page.getByRole("dialog").getByRole("table", { name: "Namespace routes" })).toBeVisible()
  await expect(page.getByRole("dialog").getByRole("button", { name: "Open lab-h" })).toBeVisible()

  const container = await openDevice(page, "veth6e4f828")
  await expect(
    container.getByRole("link", { name: "Investigate a connection from postgres" }),
  ).toHaveAttribute("href", "/network/investigate?container=postgres")
})

test("a managed bridge reads as a switch and a port's VLAN policy is applied and confirmed", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  const bridge = await openDevice(page, "jd-lab")
  const sw = bridge.locator("[data-slot=panel]").filter({ hasText: "Switch" }).first()
  await expect(sw.getByText("on · 802.1Q")).toBeVisible()
  await expect(sw.getByRole("table", { name: "VLANs by port" })).toContainText("native 1")
  await expect(sw.getByRole("table", { name: "Forwarding database" })).toContainText(
    "02:42:0a:00:04:02",
  )
  await expect(bridge.getByText("jd-lab's own VLANs")).toBeVisible()

  const port = await openDevice(page, "vlan30")
  const policy = port.locator("[data-slot=panel]").filter({ hasText: "VLANs on jd-lab" })
  await policy.getByLabel("Native VLAN").fill("10")
  await policy.getByLabel("Tagged VLANs").fill("20, 30-31")
  await policy.getByRole("button", { name: "Apply VLANs" }).click()
  const confirm = page.getByRole("alertdialog").or(page.getByRole("dialog").filter({ hasText: "Take VLAN 1 off vlan30" }))
  await expect(page.getByText("Take VLAN 1 off vlan30")).toBeVisible()
  await confirm.getByRole("button", { name: "Apply", exact: true }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "PUT",
    path: "/network/links/vlan30/vlans",
    body: {
      vlans: [
        { vid: 10, pvid: true, untagged: true },
        { vid: 20 },
        { vid: 30 },
        { vid: 31 },
      ],
    },
  })
})

test("a refused bridge move is previewed with what it would leave behind and cannot be applied", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/links/vlan30/master/preview": {
        device: "vlan30",
        allowed: false,
        refusal: "vlan30 is a port of jd-lab, which carries this server's default route",
        persisted: true,
        steps: ["ip link set vlan30 nomaster"],
        effects: [
          {
            kind: "path",
            detail:
              "vlan30 stops receiving the frames jd-lab switches; whatever reached it through jd-lab stops reaching it.",
          },
        ],
      },
    },
  })
  const sheet = await openDevice(page, "vlan30")
  await sheet.getByRole("combobox", { name: "Bridge membership" }).click()
  await page.getByRole("option", { name: "No bridge" }).click()
  const preview = sheet.getByLabel("What the move would do")
  await expect(preview.getByText("Refused")).toBeVisible()
  await expect(preview.getByText(/carries this server's default route/)).toBeVisible()
  await expect(preview.getByText(/stops receiving the frames jd-lab switches/)).toBeVisible()
  await expect(
    sheet.locator("[data-slot=panel]").filter({ hasText: "Bridge membership" }).getByRole("button", { name: "Apply" }),
  ).toBeDisabled()
})

test("a VXLAN's readiness names its checks and limits and edits its flood ends", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  const vx = {
    ...links.find((l) => l.name === "wg0"),
    name: "vx42",
    index: 41,
    kind: "vxlan",
    role: "tunnel",
    owner: "just-dashboard",
    managed: true,
    guard: undefined,
    vni: 42,
    remote: "198.51.100.7",
    local: "203.0.113.20",
    port: 4789,
    remotes: ["198.51.100.8"],
    addresses: [],
  }
  await mockNetwork(page, mutations, {
    overrides: {
      "/network/links": [...links, vx],
      "/network/links/vx42/readiness": {
        name: "vx42",
        kind: "vxlan",
        checkedAt: iso(0),
        checks: [
          { id: "underlay", label: "Underlay device", state: "ok", detail: "vx42 sends from ens3, which is up with a carrier." },
          {
            id: "route:198.51.100.8",
            label: "Route to 198.51.100.8",
            state: "failed",
            detail: "The route to 198.51.100.8 leaves through vx42 itself, so the tunnel would carry its own packets.",
          },
          { id: "mtu", label: "MTU headroom", state: "warning", detail: "set it to 1450 or less" },
        ],
        limits: ["VXLAN is neither encrypted nor authenticated: anything that can reach UDP 4789 here can inject frames into the segment."],
      },
    },
  })
  const sheet = await openDevice(page, "vx42")
  const checks = sheet.getByRole("list", { name: "Readiness checks" })
  await expect(checks.getByText("Failing")).toBeVisible()
  await expect(checks.getByText(/leaves through vx42 itself/)).toBeVisible()
  await expect(sheet.getByText(/neither encrypted nor authenticated/)).toBeVisible()
  const ends = sheet.getByLabel("Further ends")
  await expect(ends).toHaveValue("198.51.100.8")
  await ends.fill("198.51.100.8, 198.51.100.9")
  await sheet.getByRole("button", { name: "Save ends" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "PUT",
    path: "/network/links/vx42/remotes",
    body: { remotes: ["198.51.100.8", "198.51.100.9"] },
  })
  await ends.fill("2001:db8::9")
  await expect(sheet.getByText("Every end is IPv4, like the remote.")).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save ends" })).toBeDisabled()
})

test("bridges, macvlans and dummies explain themselves before they are made", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/interfaces")
  await loaded(page)
  await page.getByRole("button", { name: "New device", exact: true }).click()
  const form = page.getByRole("dialog")
  await form.getByRole("button", { name: "Make a Macvlan", exact: true }).click()
  await expect(form.getByText(/This server cannot reach them through the card/)).toBeVisible()
  await expect(form.getByText("Provider MAC filtering")).toBeVisible()
  await form.getByRole("button", { name: "Make a Dummy", exact: true }).click()
  await expect(form.getByText("What a dummy is for")).toBeVisible()
  await form.getByRole("button", { name: "Make a Bridge", exact: true }).click()
  await form.getByRole("switch", { name: "Filter by VLAN" }).click()
  await form.getByRole("switch", { name: "Multicast snooping" }).click()
  await form.getByRole("button", { name: /^Create / }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0].body).toMatchObject({
    kind: "bridge",
    stp: false,
    vlanFiltering: true,
    multicastSnooping: false,
  })
})

test("a dummy's sheet hands a route into it to the routing page", async ({ page }) => {
  const dummy = {
    ...links.find((l) => l.name === "vlan30"),
    name: "svc0",
    index: 42,
    kind: "dummy",
    role: "virtual",
    master: undefined,
    parent: undefined,
    vlanId: undefined,
    addresses: [{ cidr: "10.255.0.1/32", family: "inet", scope: "global", managed: true }],
  }
  await mockNetwork(page, [], {
    overrides: {
      "/network/links": [...links, dummy],
      "/network/links/svc0/readiness": {
        name: "svc0",
        kind: "dummy",
        checkedAt: iso(0),
        checks: [
          { id: "routes", label: "Routes into it", state: "info", detail: "No route sends traffic into svc0." },
        ],
        limits: [],
      },
    },
  })
  const sheet = await openDevice(page, "svc0")
  await sheet.getByRole("link", { name: "Route a destination into svc0" }).click()
  await expect(page).toHaveURL(/\/network\/routing/)
  const form = page.getByRole("dialog")
  await expect(form.getByRole("combobox").filter({ hasText: "svc0" })).toBeVisible()
})

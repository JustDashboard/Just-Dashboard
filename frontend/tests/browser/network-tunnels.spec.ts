import { expect, test } from "@playwright/test"
import { loaded, mockNetwork, type Mutation } from "./network-fixture"

for (const [kind, title, remote, local, limit] of [
  ["gre", "GRE tunnel", "198.51.100.7", "203.0.113.20", "TTL"],
  ["gretap", "GRE tap", "198.51.100.7", "203.0.113.20", "TTL"],
  ["ip6gre", "IPv6 GRE tunnel", "2001:db8::7", "2001:db8::20", "Hop limit"],
  ["ip6gretap", "IPv6 GRE tap", "2001:db8::7", "2001:db8::20", "Hop limit"],
] as const) {
  test(`${title} sends its optional key and outer packet limit without implying encryption`, async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations)
    await page.goto("/network/interfaces")
    await loaded(page)
    await page.getByRole("button", { name: "New device", exact: true }).click()
    const form = page.getByRole("dialog")
    await form.getByRole("button", { name: `Make a ${title}`, exact: true }).click()
    await form.getByLabel("Name", { exact: true }).fill("jd-tunnel")
    await form.getByLabel("Remote end", { exact: true }).fill(remote)
    await form.getByLabel("Local end", { exact: true }).fill(local)
    await form.getByLabel(limit, { exact: true }).fill("64")
    await form.getByLabel("Tunnel key", { exact: true }).fill("4294967295")
    await expect(form.getByText("Not encrypted", { exact: true })).toBeVisible()
    await expect(
      form.getByText("This is an identifier, not encryption.", { exact: false }),
    ).toBeVisible()
    await form.getByRole("button", { name: "Create jd-tunnel", exact: true }).click()
    await expect.poll(() => mutations.length).toBe(1)
    expect(mutations[0]).toEqual({
      method: "POST",
      path: "/network/links",
      body: { name: "jd-tunnel", kind, up: true, remote, local, ttl: 64, key: 4294967295 },
    })
  })
}

for (const [group, local] of [
  ["239.1.1.1", "203.0.113.20"],
  ["ff05::42", "2001:db8::20"],
] as const) {
  test(`VXLAN sends multicast ${group} with its selected card and no stale unicast end`, async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations)
    await page.goto("/network/interfaces")
    await loaded(page)
    await page.getByRole("button", { name: "New device", exact: true }).click()
    const form = page.getByRole("dialog")
    await form.getByRole("button", { name: "Make a VXLAN", exact: true }).click()
    await form.getByLabel("VNI", { exact: true }).fill("42")
    await form.getByLabel("Remote end", { exact: true }).fill("198.51.100.7")
    await form.getByRole("combobox", { name: "Destination mode", exact: true }).click()
    await page.getByRole("option", { name: "Multicast group", exact: true }).click()
    await form.getByLabel("Multicast group", { exact: true }).fill(group)
    await form.getByLabel("Local end", { exact: true }).fill(local)
    const create = form.getByRole("button", { name: "Create vx42", exact: true })
    await expect(create).toBeDisabled()
    await expect(form.getByText("Choose the network card that sends to this group.")).toBeVisible()
    await form.getByRole("combobox", { name: "Send from", exact: true }).click()
    await page.getByRole("option", { name: "ens3 · uplink", exact: true }).click()
    await expect(form.getByText("Multicast underlay not verified", { exact: true })).toBeVisible()
    await create.click()
    await expect.poll(() => mutations.length).toBe(1)
    expect(mutations[0]).toEqual({
      method: "POST",
      path: "/network/links",
      body: {
        name: "vx42",
        kind: "vxlan",
        up: true,
        vni: 42,
        port: 4789,
        parent: "ens3",
        group,
        local,
      },
    })
  })
}

test("invalid multicast groups and mismatched local families cannot submit a VXLAN", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/interfaces")
  await loaded(page)
  await page.getByRole("button", { name: "New device", exact: true }).click()
  const form = page.getByRole("dialog")
  await form.getByRole("button", { name: "Make a VXLAN", exact: true }).click()
  await form.getByLabel("VNI", { exact: true }).fill("42")
  await form.getByRole("combobox", { name: "Destination mode", exact: true }).click()
  await page.getByRole("option", { name: "Multicast group", exact: true }).click()
  await form.getByRole("combobox", { name: "Send from", exact: true }).click()
  await page.getByRole("option", { name: "ens3 · uplink", exact: true }).click()
  const group = form.getByLabel("Multicast group", { exact: true })
  const create = form.getByRole("button", { name: "Create vx42", exact: true })
  for (const value of ["198.51.100.7", "ff05::42%ens3", "239.1.1.1/32"]) {
    await group.fill(value)
    await expect(group).toHaveAttribute("aria-invalid", "true")
    await expect(create).toBeDisabled()
  }
  await group.fill("ff05::42")
  await form.getByLabel("Local end", { exact: true }).fill("203.0.113.20")
  await expect(form.getByLabel("Local end", { exact: true })).toHaveAttribute(
    "aria-invalid",
    "true",
  )
  await expect(
    form.getByText("The local and destination addresses must use the same address family."),
  ).toBeVisible()
  await expect(create).toBeDisabled()
  expect(mutations).toEqual([])
})

test("malformed GRE key and hop limits cannot fall back to kernel defaults", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/interfaces")
  await loaded(page)
  await page.getByRole("button", { name: "New device", exact: true }).click()
  const form = page.getByRole("dialog")
  await form.getByRole("button", { name: "Make a IPv6 GRE tap", exact: true }).click()
  await form.getByLabel("Name", { exact: true }).fill("jd-tunnel")
  await form.getByLabel("Remote end", { exact: true }).fill("2001:db8::7")
  const create = form.getByRole("button", { name: "Create jd-tunnel", exact: true })
  await form.getByLabel("Tunnel key", { exact: true }).fill("4294967296")
  await expect(create).toBeDisabled()
  await expect(form.getByLabel("Tunnel key", { exact: true })).toHaveAttribute(
    "aria-invalid",
    "true",
  )
  await form.getByLabel("Tunnel key", { exact: true }).fill("7")
  await form.getByLabel("Hop limit", { exact: true }).fill("64.5")
  await expect(create).toBeDisabled()
  await expect(form.getByLabel("Hop limit", { exact: true })).toHaveAttribute(
    "aria-invalid",
    "true",
  )
  await form.getByLabel("Hop limit", { exact: true }).fill("0")
  await expect(create).toBeEnabled()
  expect(mutations).toEqual([])
})

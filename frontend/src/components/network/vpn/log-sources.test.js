import { expect, test } from "bun:test"
import { vpnLogSources } from "./log-sources"

const vpn = (over = {}) => ({
  wireguard: { installed: true, systemd: true, interfaces: [] },
  tailscale: { installed: false },
  headscale: { installed: false },
  ...over,
})

const tunnel = (name, over = {}) => ({ name, configured: true, ...over })

test("each configured tunnel is read in its wg-quick unit, where systemd brings it up", () => {
  const sources = vpnLogSources(
    vpn({
      wireguard: {
        installed: true,
        systemd: true,
        interfaces: [tunnel("wg0"), tunnel("office"), tunnel("wg9", { configured: false })],
      },
    }),
  )
  expect(sources.map((s) => [s.id, s.label, s.product])).toEqual([
    ["journal:wg-quick@wg0.service", "wg-quick@wg0.service", "wireguard"],
    ["journal:wg-quick@office.service", "wg-quick@office.service", "wireguard"],
  ])
  // Without systemd there is no unit, whatever the tunnels are.
  expect(
    vpnLogSources(
      vpn({ wireguard: { installed: true, systemd: false, interfaces: [tunnel("wg0")] } }),
    ),
  ).toEqual([])
})

test("tailscaled when Tailscale is installed, and Headscale's container when it has one", () => {
  const sources = vpnLogSources(
    vpn({
      tailscale: { installed: true },
      headscale: { installed: true, container: "headscale" },
    }),
  )
  expect(sources.map((s) => [s.id, s.kind, s.product])).toEqual([
    ["journal:tailscaled.service", "journal", "tailscale"],
    ["docker:headscale", "docker", "headscale"],
  ])
  expect(vpnLogSources(vpn({ headscale: { installed: true } }))).toEqual([])
  expect(sources.some((s) => s.lens)).toBe(false)
})

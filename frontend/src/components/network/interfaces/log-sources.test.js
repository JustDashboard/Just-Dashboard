import { expect, test } from "bun:test"
import { interfaceLogSources } from "./log-sources"

const unit = (name, activeState) => ({ name, description: name, activeState })

test("the kernel's ring stands alone until the units are known, and where they cannot be", () => {
  const alone = interfaceLogSources(undefined)
  expect(alone).toEqual([
    {
      id: "kernel:",
      label: "Kernel ring",
      kind: "kernel",
      lens: "kernel",
      product: "linux",
      detail: "Links coming up and going down, drivers, the OOM killer",
    },
  ])
  expect(interfaceLogSources([])).toEqual(alone)
})

test("the daemon that manages the links is read beside the ring, whichever one runs", () => {
  const units = [
    unit("sshd.service", "active"),
    unit("systemd-networkd.service", "active"),
    unit("NetworkManager.service", "inactive"),
  ]
  expect(interfaceLogSources(units).map((s) => s.id)).toEqual([
    "kernel:",
    "journal:systemd-networkd.service",
  ])
  const both = [
    unit("NetworkManager.service", "failed"),
    unit("systemd-networkd.service", "active"),
  ]
  expect(interfaceLogSources(both).map((s) => s.id)).toEqual([
    "kernel:",
    "journal:NetworkManager.service",
    "journal:systemd-networkd.service",
  ])
})

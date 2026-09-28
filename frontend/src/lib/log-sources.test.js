import { expect, test } from "bun:test"
import {
  dockerSource,
  fileSource,
  journalIdSource,
  journalSource,
  kernelSource,
  pm2Source,
  sourceKindOf,
  stackSource,
} from "./log-sources"

// These are the strings the backend's `parseLogTarget` reads; a page that
// spells one differently opens a different session key, or a 400.
test("each kind is its prefix and its target", () => {
  expect(dockerSource("abc123")).toBe("docker:abc123")
  expect(fileSource("/var/log/auth.log")).toBe("file:/var/log/auth.log")
  expect(journalSource("ssh.service")).toBe("journal:ssh.service")
  expect(journalSource()).toBe("journal:")
  expect(journalIdSource(["sshd", "sshd-session", "sudo"])).toBe(
    "journal-id:sshd,sshd-session,sudo",
  )
  expect(kernelSource()).toBe("kernel:")
  expect(stackSource("shop")).toBe("stack:shop")
})

test("a PM2 source escapes the account and the name, which may hold slashes", () => {
  expect(pm2Source("bob", 0, "same-name")).toBe("pm2:bob/0/same-name")
  const id = pm2Source("svc/deploy", 3, "api worker/1")
  expect(id).toBe("pm2:svc%2Fdeploy/3/api%20worker%2F1")
  const [daemon, n, name] = id.slice("pm2:".length).split("/")
  expect([decodeURIComponent(daemon), Number(n), decodeURIComponent(name)]).toEqual([
    "svc/deploy",
    3,
    "api worker/1",
  ])
})

test("the kind is read back off the prefix", () => {
  expect(sourceKindOf(stackSource("shop"))).toBe("stack")
  expect(sourceKindOf(fileSource("/var/log/x:y"))).toBe("file")
  expect(sourceKindOf("nonsense")).toBe("")
})

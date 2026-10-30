import { expect, test } from "bun:test"
import { existsSync } from "node:fs"
import { join } from "node:path"
import { ProductGlyph } from "../product-logo"
import { accountProduct, groupProduct, shellProduct } from "./marks"

const PUBLIC = join(import.meta.dir, "../../../public")

test("a daemon's account is the product that runs under it", () => {
  expect(accountProduct({ username: "postgres", system: true })).toBe("postgresql")
  expect(accountProduct({ username: "gitlab-runner", system: true })).toBe("gitlab")
  expect(accountProduct({ username: "_apt", system: true })).toBe("debian")
  expect(accountProduct({ username: "dockremap", system: true })).toBe("docker")
})

test("an account nothing names, or a person's, is no product", () => {
  expect(accountProduct({ username: "www-data", system: true })).toBeUndefined()
  expect(accountProduct({ username: "systemd-network", system: true })).toBeUndefined()
  // A person called git is a person.
  expect(accountProduct({ username: "git", system: false })).toBeUndefined()
})

test("a shell and a root-equivalent group are drawn as what they are", () => {
  expect(shellProduct("/bin/bash")).toBe("shell-bash")
  expect(shellProduct("/usr/bin/zsh")).toBe("shell-zsh")
  expect(shellProduct("/usr/sbin/nologin")).toBeUndefined()
  expect(groupProduct("docker")).toBe("docker")
  expect(groupProduct("lxd")).toBe("lxd")
  expect(groupProduct("adm")).toBeUndefined()
  for (const id of ["shell-bash", "shell-zsh", "shell-fish", "lxd"]) {
    const src = ProductGlyph({ id })?.props.src
    expect(src !== undefined && existsSync(join(PUBLIC, src))).toBe(true)
  }
})

test("every account product has its file", () => {
  for (const username of ["postgres", "redis", "grafana", "jenkins", "caddy", "ollama"]) {
    const id = accountProduct({ username, system: true })
    const src = ProductGlyph({ id })?.props.src
    expect({ id, bundled: src !== undefined && existsSync(join(PUBLIC, src)) }).toEqual({
      id,
      bundled: true,
    })
  }
})

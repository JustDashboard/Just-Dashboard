import { expect, test } from "bun:test"
import { existsSync } from "node:fs"
import { join } from "node:path"
import { ProductGlyph } from "../product-logo"
import { managerProduct, originProduct, packageProduct, sectionGlyph, versionChange } from "./marks"
import { Layers, Puzzle } from "../icons"

const PUBLIC = join(import.meta.dir, "../../../public")

test("a package is the product its name says, whatever its version and packaging", () => {
  const cases = {
    nginx: "nginx",
    "nginx-common": "nginx",
    "libnginx-mod-stream": "nginx",
    "postgresql-16": "postgresql",
    "postgresql-client-16": "postgresql",
    "postgresql16-server": "postgresql",
    libpq5: "postgresql",
    "python3-requests": "python",
    "python3.12": "python",
    "libpython3.12-stdlib": "python",
    "python3-certbot-nginx": "lets-encrypt",
    certbot: "lets-encrypt",
    "php8.3-fpm": "php",
    "docker-ce": "docker",
    "docker.io": "docker",
    "containerd.io": "docker",
    "docker-compose-plugin": "docker-compose",
    nodejs: "nodejs",
    "node-lodash": "nodejs",
    "golang-go": "go",
    "openjdk-17-jre-headless": "java",
    "linux-image-6.8.0-45-generic": "linux",
    "ubuntu-minimal": "ubuntu",
    "debian-archive-keyring": "debian",
    fail2ban: "fail2ban",
    gh: "github",
    "google-chrome-stable": "chrome",
    "google-cloud-cli": "google-cloud",
    "amd64-microcode": "amd",
    "intel-microcode": "intel",
    "redis-server": "redis",
    "mariadb-server": "mariadb",
    libcurl4t64: "curl",
    "sqlite3:amd64": "sqlite",
    apt: "debian",
  }
  for (const [name, product] of Object.entries(cases)) {
    expect({ name, product: packageProduct(name) }).toEqual({ name, product })
  }
})

test("a package whose name says nothing is no product", () => {
  for (const name of ["libc6", "libtinfo6", "openssl", "gcc-13", "x11-common", "htop", "bash"]) {
    expect({ name, product: packageProduct(name) }).toEqual({ name, product: undefined })
  }
  // A library is its own project, not the product its name begins with.
  expect(packageProduct("libgit2-1.7")).toBeUndefined()
  expect(packageProduct("libdocker")).toBeUndefined()
})

test("a package no product names is drawn as its section", () => {
  expect(sectionGlyph("libs")).toBe(Layers)
  expect(sectionGlyph("universe/libs")).toBe(Layers)
  expect(sectionGlyph(undefined)).toBe(Puzzle)
  expect(sectionGlyph("Unspecified")).toBe(Puzzle)
})

test("an upgrade's origin is the archive that published it", () => {
  expect(originProduct("Ubuntu:24.04/noble-security [amd64]")).toBe("ubuntu")
  expect(originProduct("Debian:12/bookworm-security")).toBe("debian")
  expect(originProduct("apt.postgresql.org:noble-pgdg")).toBe("postgresql")
  expect(originProduct("pgdg16")).toBe("postgresql")
  expect(originProduct("docker-ce-stable")).toBe("docker")
  expect(originProduct("LP-PPA-someone:noble")).toBeUndefined()
  expect(originProduct(undefined)).toBeUndefined()
})

test("a manager is its distribution's only where it has one", () => {
  expect(managerProduct("apt")).toBe("debian")
  expect(managerProduct("dnf")).toBe("fedora")
  expect(managerProduct("zypper")).toBeUndefined()
})

test("a version change parts at the field that moved", () => {
  expect(versionChange("3.0.13-0ubuntu3", "3.0.13-0ubuntu3.4")).toEqual({
    kept: "3.0.13-0ubuntu3",
    changed: ".4",
  })
  expect(versionChange("1.24.0", "1.25.0")).toEqual({ kept: "1.", changed: "25.0" })
  expect(versionChange("8.5.0-2ubuntu10.1", "8.5.0-2ubuntu10.4")).toEqual({
    kept: "8.5.0-2ubuntu10.",
    changed: "4",
  })
  expect(versionChange(undefined, "2.0")).toEqual({ kept: "", changed: "2.0" })
  expect(versionChange("2.0", "2.0")).toEqual({ kept: "2.0", changed: "" })
})

test("every product a package reader returns has its file", () => {
  const names = [
    "nginx",
    "postgresql-16",
    "python3",
    "certbot",
    "docker-compose-plugin",
    "linux-image-generic",
    "ubuntu-minimal",
    "fail2ban",
    "gh",
    "google-chrome-stable",
    "amd64-microcode",
    "phpmyadmin",
    "cloudflared",
    "yarnpkg",
  ]
  const ids = [
    ...names.map(packageProduct),
    originProduct("Ubuntu:24.04"),
    managerProduct("pacman"),
    managerProduct("apk"),
  ]
  for (const id of ids) {
    const src = ProductGlyph({ id })?.props.src
    expect({ id, bundled: src !== undefined && existsSync(join(PUBLIC, src)) }).toEqual({
      id,
      bundled: true,
    })
  }
})

test("a field one version cuts short is not kept", () => {
  expect(versionChange("1.24", "1.2.1")).toEqual({ kept: "1.", changed: "2.1" })
})

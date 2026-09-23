import { describe, expect, test } from "bun:test"
import { defaultFolderColour, fileKind, folderColourOf, folderKind } from "./file-icon"

describe("fileKind", () => {
  test("a format is drawn as its own logo, with its extension on the band", () => {
    expect(fileKind("app.ts")).toMatchObject({ logo: "typescript.svg", ext: "ts" })
    expect(fileKind("Page.tsx")).toMatchObject({ logo: "react.svg", ext: "tsx" })
    expect(fileKind("main.go").logo).toBe("go.svg")
  })

  test("a name known whole wins over its extension, and keeps it for the band", () => {
    expect(fileKind("package.json")).toMatchObject({ logo: "npm.svg", ext: "json" })
    expect(fileKind("docker-compose.yml")).toMatchObject({ label: "Compose file", ext: "yml" })
    expect(fileKind("Dockerfile")).toMatchObject({ logo: "docker.svg", tag: "DOCK" })
    expect(fileKind("bun.lock").tag).toBe("LOCK")
  })

  test("a saved copy is still its format, and a tarball is an archive", () => {
    expect(fileKind("site.conf.bak")).toMatchObject({ label: "Configuration", tag: "BAK" })
    expect(fileKind("release.tar.zst").label).toBe("Archive")
  })

  test("a dotfile's name is not an extension", () => {
    expect(fileKind(".lesshst").ext).toBeUndefined()
    expect(fileKind(".env.production").label).toBe("Environment file")
    expect(fileKind("id_ed25519").label).toBe("SSH private key")
  })
})

describe("folders", () => {
  test("a folder whose name says what it holds carries it", () => {
    expect(folderKind(".git").mark).toBe("git.svg")
    expect(folderKind("node_modules")).toMatchObject({ mark: "nodejs.svg", colour: "graphite" })
    expect(folderKind("ubuntu", "/home/ubuntu").label).toBe("Home folder")
    expect(folderKind("ubuntu", "/srv/ubuntu").label).toBe("Folder")
  })

  test("a label wins, an unknown one is ignored, and the default is by name", () => {
    const labels = { "/srv/app": "red", "/srv/odd": "chartreuse" }
    expect(folderColourOf(labels, "/srv/app", "app")).toBe("red")
    expect(folderColourOf(labels, "/srv/odd", "odd")).toBe("blue")
    expect(folderColourOf(labels, "/srv/dist", "dist")).toBe("graphite")
    expect(defaultFolderColour("projects")).toBe("blue")
  })
})

import { describe, expect, test } from "bun:test"
import {
  EVERYTHING,
  MASK,
  SUMMARY,
  filterJson,
  inspectSections,
  leaves,
  maskInspect,
  openingSection,
  sizeOf,
  stringKind,
} from "./inspect"

const doc = {
  Id: "1111111111111111",
  Name: "/web",
  Args: ["-g", "daemon off;"],
  Created: "2026-10-04T10:00:00Z",
  Config: {
    Image: "nginx:alpine",
    Env: ["PATH=/usr/bin", "JD_MASTER_KEY=s3cr3t-master", "JD_BOOTSTRAP_PASSWORD=hunter2"],
    Labels: { "com.docker.compose.project": "shop" },
  },
  State: { Status: "running", ExitCode: 0, Running: true, Health: null },
  Mounts: [{ Type: "volume", Destination: "/data" }],
  HostConfig: { Memory: 0, PortBindings: {} },
  Platform: "linux",
}

describe("maskInspect", () => {
  // The same names the Environment tab hides, and nothing else in the document.
  test("hides credential-shaped environment values and counts them", () => {
    const { doc: masked, masked: count } = maskInspect(doc)
    expect(count).toBe(2)
    expect(masked.Config.Env).toEqual([
      "PATH=/usr/bin",
      `JD_MASTER_KEY=${MASK}`,
      `JD_BOOTSTRAP_PASSWORD=${MASK}`,
    ])
    expect(JSON.stringify(masked)).not.toContain("s3cr3t-master")
    expect(JSON.stringify(masked)).not.toContain("hunter2")
    // The original is untouched, for the deliberate reveal.
    expect(doc.Config.Env[1]).toBe("JD_MASTER_KEY=s3cr3t-master")
  })

  test("leaves a document with nothing to hide as it is", () => {
    const plain = { Config: { Env: ["PATH=/usr/bin"] } }
    expect(maskInspect(plain)).toEqual({ doc: plain, masked: 0 })
    expect(maskInspect({ Id: "x" }).masked).toBe(0)
  })
})

describe("inspectSections", () => {
  test("gathers the scalar fields, then the nested ones in the order they are read", () => {
    const sections = inspectSections(doc)
    expect(sections.map((section) => section.key)).toEqual([
      SUMMARY,
      "Config",
      "State",
      "HostConfig",
      "Mounts",
      EVERYTHING,
    ])
    // An array of plain values is a fact beside the id, not a section.
    expect(Object.keys(sections[0].value)).toEqual(["Id", "Name", "Args", "Created", "Platform"])
    expect(sections.at(-1).value).toBe(doc)
  })

  test("lists a nested field it does not name after the ones it does", () => {
    const sections = inspectSections({ Zeta: { a: 1 }, Config: {}, Alpha: [{ b: 2 }] })
    expect(sections.map((section) => section.key)).toEqual(["Config", "Alpha", "Zeta", EVERYTHING])
  })

  test("opens on the configuration, or the first section a small document has", () => {
    expect(openingSection(inspectSections(doc))).toBe("Config")
    expect(openingSection(inspectSections({ Id: "x" }))).toBe(SUMMARY)
    expect(openingSection(inspectSections({}))).toBe(EVERYTHING)
  })
})

describe("sizeOf", () => {
  test("counts keys, items, or one value", () => {
    expect(sizeOf(doc.Config)).toBe(3)
    expect(sizeOf(doc.Config.Env)).toBe(3)
    expect(sizeOf("x")).toBe(1)
    expect(sizeOf(null)).toBe(1)
  })
})

describe("filterJson", () => {
  test("keeps a matching value with the path down to it", () => {
    expect(filterJson(doc.Config, "usr/bin")).toEqual({ Env: ["PATH=/usr/bin"] })
    expect(filterJson(doc, "NGINX")).toEqual({ Config: { Image: "nginx:alpine" } })
  })

  test("keeps everything under a key that matches", () => {
    expect(filterJson(doc, "labels")).toEqual({
      Config: { Labels: { "com.docker.compose.project": "shop" } },
    })
  })

  test("finds a number or a boolean by its text, and nothing for no match", () => {
    expect(filterJson(doc.State, "true")).toEqual({ Running: true })
    expect(filterJson(doc, "no such field")).toBeUndefined()
    expect(filterJson(doc, "  ")).toBe(doc)
  })

  // Searched after masking, which is what the tab hands it.
  test("cannot find a masked credential by its real value", () => {
    const { doc: masked } = maskInspect(doc)
    expect(filterJson(masked, "s3cr3t")).toBeUndefined()
    expect(filterJson(masked, "JD_MASTER_KEY")).toEqual({
      Config: { Env: [`JD_MASTER_KEY=${MASK}`] },
    })
  })

  test("counts what a narrowed section still holds", () => {
    expect(leaves(filterJson(doc, "usr"))).toBe(1)
    expect(leaves(doc.State)).toBe(4)
    expect(leaves(undefined)).toBe(0)
  })
})

describe("stringKind", () => {
  test("draws paths and addresses as paths and ids as digests", () => {
    expect(stringKind("/var/lib/docker/volumes")).toBe("path")
    expect(stringKind("unix:///var/run/docker.sock")).toBe("path")
    expect(stringKind("sha256:0123456789abcdef")).toBe("digest")
    expect(stringKind("1111111111111111")).toBe("digest")
    expect(stringKind("running")).toBe("string")
    // A timestamp keeps its literal text and its plain hue.
    expect(stringKind("2026-10-04T10:00:00Z")).toBe("string")
  })
})

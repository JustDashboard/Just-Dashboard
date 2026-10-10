import { isSecretEnvKey } from "@/components/docker/container-tables"

/**
 * The Engine's inspect document as the Inspect tab reads it: masked where it
 * holds a credential, split into the sections a reader looks for, and
 * narrowed to what a filter matches. Pure, so `inspect.test.js` can hold each
 * answer still.
 */

export type Json = null | boolean | number | string | Json[] | { [key: string]: Json }

export const MASK = "••••••••••••"

/**
 * Masks credential-shaped environment values inside a decoded inspect document.
 *
 * Mirrors what the server does for anyone below system.admin. For an admin the
 * server sends the real values — correctly, they are allowed to see them — but
 * the Environment tab still puts them behind a deliberate reveal, and one tab
 * over printing the same secrets unprompted made that gesture worthless. The
 * threat here is a screen, not a permission.
 *
 * Only the place the Engine puts an environment is walked, not every string in
 * the document: a blanket scrub mangles labels and commands that legitimately
 * contain the word "key".
 */
export function maskInspect(doc: Record<string, unknown>): {
  doc: Record<string, unknown>
  masked: number
} {
  let masked = 0
  const config = doc.Config
  if (!config || typeof config !== "object") return { doc, masked }
  const env = (config as Record<string, unknown>).Env
  if (!Array.isArray(env)) return { doc, masked }

  const maskedEnv = env.map((entry) => {
    if (typeof entry !== "string") return entry
    const eq = entry.indexOf("=")
    if (eq === -1) return entry
    const name = entry.slice(0, eq)
    if (!isSecretEnvKey(name)) return entry
    masked++
    return `${name}=${MASK}`
  })
  if (masked === 0) return { doc, masked }
  return {
    doc: { ...doc, Config: { ...(config as Record<string, unknown>), Env: maskedEnv } },
    masked,
  }
}

export type InspectSection = {
  /** The rail's key: a top-level field of the document, or one of the two made here. */
  key: string
  label: string
  /** What a reader goes into it for, in a few words. */
  hint?: string
  value: Json
}

/** The container's own fields — its id, name, image and times — gathered as one section. */
export const SUMMARY = "Container"
/** The whole document, for a field nobody chose a section for. */
export const EVERYTHING = "Everything"

/** The sections an operator opens, in the order they are asked about. */
const KNOWN: Record<string, string> = {
  Config: "image, command, environment, labels",
  State: "status, health, exit",
  HostConfig: "limits, ports, restart policy",
  NetworkSettings: "addresses and ports",
  Mounts: "volumes and folders",
  GraphDriver: "the layers on disk",
}

/**
 * The document as the rail lists it: the container's own scalar fields as
 * one section first, the sections the Engine nests in the order a reader
 * wants them, anything else it nests after them by name, and the whole
 * document last. A field the rail does not name is still under Everything.
 */
export function inspectSections(doc: Record<string, Json>): InspectSection[] {
  const summary: Record<string, Json> = {}
  const nested: string[] = []
  for (const [key, value] of Object.entries(doc)) {
    if (nests(value)) nested.push(key)
    else summary[key] = value
  }
  const known = Object.keys(KNOWN).filter((key) => nested.includes(key))
  const rest = nested.filter((key) => !(key in KNOWN)).sort((a, b) => a.localeCompare(b))
  const sections: InspectSection[] = []
  if (Object.keys(summary).length > 0)
    sections.push({
      key: SUMMARY,
      label: SUMMARY,
      hint: "id, name, image, created",
      value: summary,
    })
  for (const key of [...known, ...rest])
    sections.push({ key, label: key, hint: KNOWN[key], value: doc[key] })
  sections.push({ key: EVERYTHING, label: EVERYTHING, hint: "the whole document", value: doc })
  return sections
}

/**
 * A field with structure of its own. An array of plain values — `Args` — is
 * read as one fact beside the id and the name rather than as a section.
 */
function nests(value: Json): boolean {
  if (Array.isArray(value)) return value.some((item) => item !== null && typeof item === "object")
  return value !== null && typeof value === "object"
}

/** Where the rail opens: the configuration, which is what most visits are for. */
export function openingSection(sections: InspectSection[]): string {
  return (sections.find((section) => section.key === "Config") ?? sections[0]).key
}

/** How many fields a section holds: keys for an object, items for an array, one for a value. */
export function sizeOf(value: Json): number {
  if (Array.isArray(value)) return value.length
  if (value !== null && typeof value === "object") return Object.keys(value).length
  return 1
}

/**
 * The document narrowed to what matches: a key or a plain value containing
 * the text, with the path down to it. A key that matches keeps everything
 * under it, since the reader asked for that field and not for part of it.
 * Undefined when nothing matches. The text is matched against what is drawn,
 * so a masked credential's real value can never be found by searching for it.
 */
export function filterJson(value: Json, needle: string): Json | undefined {
  const query = needle.trim().toLowerCase()
  if (!query) return value
  return narrow(value, query)
}

function narrow(value: Json, query: string): Json | undefined {
  if (Array.isArray(value)) {
    const kept = value
      .map((item) => narrow(item, query))
      .filter((item): item is Json => item !== undefined)
    return kept.length > 0 ? kept : undefined
  }
  if (value !== null && typeof value === "object") {
    const kept: Record<string, Json> = {}
    for (const [key, child] of Object.entries(value)) {
      if (key.toLowerCase().includes(query)) {
        kept[key] = child
        continue
      }
      const inner = narrow(child, query)
      if (inner !== undefined) kept[key] = inner
    }
    return Object.keys(kept).length > 0 ? kept : undefined
  }
  return String(value).toLowerCase().includes(query) ? value : undefined
}

/** How many plain values a narrowed section still holds, for the rail's count under a filter. */
export function leaves(value: Json | undefined): number {
  if (value === undefined) return 0
  if (Array.isArray(value)) return value.reduce<number>((total, item) => total + leaves(item), 0)
  if (value !== null && typeof value === "object")
    return Object.values(value).reduce<number>((total, item) => total + leaves(item), 0)
  return 1
}

const DIGEST = /^sha256:[0-9a-f]{12,}$/
const HEX = /^[0-9a-f]{12,64}$/
const PATH = /^(\/|\.\/)/
const URL_TEXT = /^[a-z][a-z0-9+.-]*:\/\//i

/**
 * What a string is, for the hue it is drawn in: a path or an address in the
 * hue the console gives a path, an id or digest stepped back, anything else a
 * string. A timestamp or a size keeps its literal text — the document is the
 * Engine's own words, and this tab is where those are read as written.
 */
export function stringKind(text: string): "path" | "digest" | "string" {
  if (PATH.test(text) || URL_TEXT.test(text)) return "path"
  if (DIGEST.test(text) || HEX.test(text)) return "digest"
  return "string"
}

import { plural } from "@/lib/format"
import { systemCensus, type System, type SystemKind } from "@/lib/server-systems"
import { censusLine } from "@/components/map/rail"

/**
 * What one canvas of systems is of, for the pieces the server map and the
 * databases map share (`canvas.tsx`, `rail.tsx`, `inspector.tsx`): how its
 * rail is grouped, what a system says of itself in a few words, and what the
 * inspector says with nothing chosen. Everything else — the shapes, the
 * wires, how it moves, what is kept of an arrangement — is one canvas's.
 */
export type MapBoard = {
  /** The canvas's name, for a screen reader. */
  label: string
  /** The rail's name, and its groups in order. A group with nothing in it is not drawn. */
  rail: {
    label: string
    group: (systems: System[]) => { label: string; systems: System[] }[]
    /** Said in the rail when there is no system at all. */
    empty: string
  }
  /** What a system holds, in a few words: under its name in the rail and on its frame. */
  census: (system: System, most?: number) => string
  /** The inspector's figures for one system. */
  holds: (system: System) => { label: string; value: string }[]
  /** The inspector with nothing chosen: what the whole canvas is. */
  whole: (systems: System[]) => {
    eyebrow: string
    title: string
    line: string
    heading: string
    facts: { label: string; value: string }[]
  }
}

/** The server map's rail, in the order the systems are found. */
const SERVER_GROUPS: { label: string; kinds: SystemKind[] }[] = [
  { label: "This dashboard", kinds: ["dashboard"] },
  { label: "Deployments", kinds: ["project"] },
  { label: "Compose stacks", kinds: ["stack"] },
  { label: "Processes", kinds: ["pm2", "service"] },
  { label: "On their own", kinds: ["container", "database"] },
  { label: "Not wired", kinds: ["loose"] },
]

const count = (n: number) => n.toLocaleString("en-US")

export const SERVER_BOARD: MapBoard = {
  label: "Map of the server's systems",
  rail: {
    label: "Systems",
    group: (systems) =>
      SERVER_GROUPS.map((group) => ({
        label: group.label,
        systems: systems.filter((s) => group.kinds.includes(s.kind)),
      })),
    empty: "Nothing on this server makes a system yet.",
  },
  census: censusLine,
  holds: (system) => {
    const c = systemCensus(system)
    return [
      { label: "Containers", value: c.containers },
      { label: "Databases", value: c.databases },
      { label: "Domains", value: c.domains },
      { label: "Volumes", value: c.volumes },
      { label: "Backups", value: c.backups },
      { label: "Checkouts", value: c.checkouts },
    ]
      .filter((f) => f.value > 0)
      .map((f) => ({ label: f.label, value: String(f.value) }))
  },
  whole: (systems) => {
    // Each thing once, however many systems draw it.
    const seen = new Map<string, string>()
    for (const s of systems) {
      for (const n of s.nodes) if (n.kind !== "logs") seen.set(n.itemId, n.kind)
      for (const f of s.frames) seen.set(f.itemId, "stacks")
    }
    const of = (...kinds: string[]) => [...seen.values()].filter((k) => kinds.includes(k)).length
    return {
      eyebrow: "This server",
      title: plural(systems.length, "system"),
      line: "Each one is drawn on its own, with what it is wired to",
      heading: "On this server",
      facts: [
        { label: "Deployments", value: of("projects") },
        { label: "Stacks", value: of("stacks") },
        { label: "Containers", value: of("containers") },
        { label: "Databases", value: of("databases") },
        { label: "Domains", value: of("domains") },
        { label: "Volumes", value: of("volumes") },
        { label: "Backups", value: of("backups") },
        { label: "Apps and services", value: of("pm2", "services") },
      ]
        .filter((t) => t.value > 0)
        .map((t) => ({ label: t.label, value: count(t.value) })),
    }
  },
}

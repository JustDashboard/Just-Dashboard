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
    /**
     * `kinds` are the node kinds a figure counts. Where a board names them
     * the figure is a row that selects those boxes on the canvas; without
     * them it is a figure.
     */
    facts: { label: string; value: string; kinds?: string[] }[]
  }
  /**
   * The system the canvas opens on when every system at once would be too
   * small to read and the reader left no place of their own. A board without
   * one opens on everything, however small.
   */
  home?: (systems: System[]) => string | undefined
  /**
   * Where what could not be read is said: `inspector` is one line per cause
   * in the inspector (`unread.ts`), and over the canvas only while the
   * inspector is not shown. Without it, each kind is a line over the canvas.
   */
  unread?: "inspector"
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
        { label: "Deployments", kinds: ["projects"] },
        { label: "Stacks", kinds: ["stacks"] },
        { label: "Containers", kinds: ["containers"] },
        { label: "Databases", kinds: ["databases"] },
        { label: "Domains", kinds: ["domains"] },
        { label: "Volumes", kinds: ["volumes"] },
        { label: "Backups", kinds: ["backups"] },
        { label: "Apps and services", kinds: ["pm2", "services"] },
      ]
        .map((t) => ({ ...t, value: of(...t.kinds) }))
        .filter((t) => t.value > 0)
        .map((t) => ({ ...t, value: count(t.value) })),
    }
  },
  // The dashboard's own system is the one every server has, and the first in
  // the rail; a server whose reading left it out opens on the first there is.
  home: (systems) => (systems.find((s) => s.kind === "dashboard") ?? systems[0])?.id,
  unread: "inspector",
}

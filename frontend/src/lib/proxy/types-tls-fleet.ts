import type { Job } from "@/lib/types"

/** A target's latest stored TLS report, as the fleet table reads it. */
export type FleetScan = {
  id: number
  grade: string
  summary: string
  daysLeft?: number
  expiring: boolean
  expired: boolean
  reachable: boolean
  negotiated?: string
  /** Critical and warning findings. */
  issues: number
  checkedAt: string
}

/** One name and port the scan of every site reaches, and why it is on the list. */
export type FleetTarget = {
  host: string
  port: number
  /** The enabled sites that serve it over TLS. */
  sites: string[]
  watched: boolean
  /** Absent for a target never scanned. */
  scan?: FleetScan
}

export type FleetView = {
  targets: FleetTarget[]
  /** The scan of every site in flight, if one is. */
  job?: Job
}

import type { ProbeResult } from "@/lib/types"

export type PathRequest = {
  sourceKind: "host" | "container" | "external"
  containerId?: string
  sourceAddress?: string
  target: string
  address?: string
  family: "inet" | "inet6"
  protocol: "tcp" | "udp"
  port: number
  mark?: string
  measure: boolean
}

export type PathEvidence = {
  id: string
  title: string
  basis: "observed" | "modeled" | "measured" | "unknown"
  state: string
  scope: string
  owner: string
  ownerPath?: string
  checkedAt: string
  summary: string
  facts: { label: string; value: string }[]
  limitations: string[]
}

export type PathResult = {
  request: PathRequest
  scope: {
    vantage: "dashboard_host" | "container_network_namespace" | "published_port"
    source: string
    sourceAddress?: string
    target: string
    address?: string
    family: "inet" | "inet6"
    protocol: "tcp" | "udp"
    port: number
    mark?: string
    limitations: string[]
  }
  startedAt: string
  endedAt: string
  addresses: string[]
  evidence: PathEvidence[]
  measurement?: ProbeResult
  comparison: string
}

export type PathSources = {
  containers: { id: string; name: string }[]
  error?: string
}

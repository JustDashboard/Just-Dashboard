export interface DNSInvestigationRequest {
  name: string
  type: string
  expectedInterface?: string
}

export interface DNSEvidenceReading {
  state: string
  basis: string
  summary: string
}

export interface DNSPolicyScope {
  index: number
  interface: string
  domains: string[]
  servers: string[]
  currentServer?: string
  activeDNS: boolean
  defaultRoute: boolean
  dnssec: string
  dnsOverTLS: string
  negativeTrustAnchors: string[]
}

export interface DNSInvestigation {
  id?: string
  version: number
  request: DNSInvestigationRequest
  startedAt: string
  endedAt: string
  owner: string
  ownerIdentity?: string
  ownerVersion?: string
  vantage: string
  answerFamily: string
  upstreamFamily: string
  policyMatch: string
  policy: DNSPolicyScope[]
  policyStable: boolean
  answerInterfaces: number[]
  answers: string[]
  records: { interfaceIndex: number; owner: string; type: number; ttl: number }[]
  nativeFlags?: string
  route: DNSEvidenceReading
  transport: DNSEvidenceReading
  trust: DNSEvidenceReading
  dnssec: DNSEvidenceReading
  nss: DNSEvidenceReading
  error?: string
  limitations: string[]
}

export interface SavedDNSEvidence {
  id: string
  request: DNSInvestigationRequest
  status: "running" | "completed" | "failed" | "interrupted"
  startedAt: string
  endedAt?: string
  result?: DNSInvestigation
}

// State text always carries its provenance. A configured switch or an unknown
// source must never get the label used for a native fresh-network measurement.
export function dnsEvidenceLabel(reading: DNSEvidenceReading): string {
  const labels: Record<string, string> = {
    unknown: "Unknown",
    modeled: "Policy candidates",
    measured: "Native answering links",
    encrypted: "Native reports encryption",
    unencrypted: "Native reports cleartext",
    validated: "Native reports DNSSEC validation",
    validation_failed: "Native reports validation failure",
    not_authenticated: "Native did not authenticate data",
    native_policy_validated: "Native strict TLS policy",
    not_measured: "Not measured",
  }
  return labels[reading.state] ?? reading.state
}

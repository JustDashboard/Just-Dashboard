/** GET /proxy/resolve?url= — which server block and location nginx picks for a URL. */
export type RouteOutcome =
  "refused" | "tls-failed" | "plain-to-tls" | "return" | "redirect" | "proxy" | "handler" | "static"

export type RouteDirective = {
  text: string
  file: string
  line: number
  inherited?: boolean
}

export type RouteStep = {
  stage: "listen" | "tls" | "server" | "location" | "answer"
  message: string
  file?: string
  line?: number
  caution?: boolean
}

export type RouteResolution = {
  url: string
  scheme: string
  host: string
  port: number
  path: string
  outcome: RouteOutcome
  server?: {
    names: string[]
    listen: string
    match: "exact" | "wildcard" | "regex" | "default_server" | "first"
    name?: string
    file: string
    line: number
  }
  location?: {
    modifier: string
    path: string
    parents: string[]
    file: string
    line: number
  }
  serves: RouteDirective[]
  steps: RouteStep[]
  certain: boolean
}

/** One forwarded port for something that does not speak HTTP. */
export type StreamSpec = {
  name: string
  listen: number
  protocol: "tcp" | "udp"
  upstream: string
  proxyProtocol: boolean
  timeout?: number
  allowFrom: string[]
}

export type StreamStatus = {
  /** Whether nginx.conf actually pulls these in. Without it they are ignored. */
  included: boolean
  snippet: string
  dir: string
  streams: StreamSpec[]
}

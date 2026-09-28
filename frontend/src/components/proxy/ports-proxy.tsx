"use client"

import { useId, useState } from "react"
import { ArrowLeftRight, Copy, Globe, MagnifyingGlass } from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import type { PortsFree } from "@/lib/types"
import { Field, FieldRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { DANGEROUS_PORTS } from "@/components/proxy/findings/shared"
import type { Socket } from "@/components/proxy/ports"
import { formatEndpoint } from "@/components/proxy/ports-list"
import type { Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

/**
 * What the proxy does with a socket: the sites forwarding to it, the stream
 * nginx listens on it for, or how many sites nginx serves on its port.
 */
export function proxyWords(socket: Socket): string | null {
  if (socket.stream) return `Stream ${socket.stream} listens here`
  const routes = socket.routes ?? []
  if (routes.length > 0) {
    const [first] = routes
    const name = `${first.serverName || first.site}${first.tls ? " (TLS)" : ""}`
    return routes.length === 1
      ? `Routed by ${name}`
      : `Routed by ${name} and ${plural(routes.length - 1, "more site")}`
  }
  if (socket.servedSites) return `Serves ${plural(socket.servedSites, "site")}`
  return null
}

export function ProxyLine({ socket }: { socket: Socket }) {
  const words = proxyWords(socket)
  if (!words) return null
  const names = (socket.routes ?? []).map((route) => route.serverName || route.site).join(", ")
  return (
    <p className="truncate text-hint text-muted-foreground" title={names || undefined}>
      {words}
    </p>
  )
}

/**
 * The verbs that put a loopback socket behind the proxy: an HTTP site for an
 * app port nothing routes to yet, and a stream for any port. Only loopback,
 * because a port already on the network needs no proxy to be reached, and
 * never the dashboard's own or the proxy's.
 */
export function proxyVerbs(socket: Socket, push: (href: string) => void): Verb[] {
  if (socket.scope !== "loopback" || socket.self || socket.stream || socket.servedSites) return []
  if (socket.process === "nginx" || socket.process === "caddy") return []
  // The IPv4 twin first: 127.0.0.1 is what a proxy config is usually written with.
  const address =
    socket.family === "ipv6" && socket.twin?.family === "ipv4"
      ? socket.twin.address
      : socket.address
  const endpoint = formatEndpoint(address, socket.port)
  const verbs: Verb[] = []
  // A database or a system port is not an HTTP app; a stream still forwards it.
  const app = socket.protocol === "tcp" && socket.port >= 1024 && !DANGEROUS_PORTS[socket.port]
  if (app && (socket.routes ?? []).length === 0) {
    const query = new URLSearchParams({ new: "1", upstream: `http://${endpoint}` })
    verbs.push({
      key: "publish",
      label: "Publish through proxy…",
      icon: Globe,
      group: "Proxy",
      run: () => push(`/proxy/sites?${query}`),
    })
  }
  const query = new URLSearchParams({
    new: "1",
    listen: "",
    upstream: endpoint,
    protocol: socket.protocol === "udp" ? "udp" : "tcp",
  })
  verbs.push({
    key: "stream",
    label: "Forward as stream…",
    icon: ArrowLeftRight,
    group: "Proxy",
    run: () => push(`/proxy/streams?${query}`),
  })
  return verbs
}

/**
 * Finds ports nothing listens on and no container keeps, a stopped one's
 * included: it binds them again when it starts. Free when asked, not
 * reserved, which the popover says.
 */
export function FreePortFinder() {
  const id = useId()
  const [protocol, setProtocol] = useState<"tcp" | "udp">("tcp")
  const [from, setFrom] = useState("8000")
  const [address, setAddress] = useState("0.0.0.0")
  const [result, setResult] = useState<PortsFree | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const find = async () => {
    setBusy(true)
    setError(null)
    try {
      setResult(
        await get<PortsFree>("/ports/free", {
          protocol,
          address,
          from: from || "1024",
          count: "5",
        }),
      )
    } catch (err) {
      setResult(null)
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm">
          <MagnifyingGlass />
          Find a free port
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 space-y-3">
        <form
          className="space-y-3"
          onSubmit={(event) => {
            event.preventDefault()
            void find()
          }}
        >
          <ToggleGroup
            type="single"
            value={protocol}
            onValueChange={(v) => v && setProtocol(v as "tcp" | "udp")}
            variant="outline"
            size="sm"
            className="w-full"
            aria-label="Protocol"
          >
            <ToggleGroupItem value="tcp" className="flex-1 text-hint">
              TCP
            </ToggleGroupItem>
            <ToggleGroupItem value="udp" className="flex-1 text-hint">
              UDP
            </ToggleGroupItem>
          </ToggleGroup>
          <FieldRow>
            <Field label="Start from" htmlFor={`${id}-from`}>
              <Input
                id={`${id}-from`}
                inputMode="numeric"
                value={from}
                onChange={(e) => setFrom(e.target.value.replace(/\D/g, "").slice(0, 5))}
              />
            </Field>
            <Field label="Address" htmlFor={`${id}-address`}>
              <Input
                id={`${id}-address`}
                value={address}
                onChange={(e) => setAddress(e.target.value.trim())}
                className="font-mono"
              />
            </Field>
          </FieldRow>
          <Button type="submit" size="sm" className="w-full" disabled={busy}>
            {busy ? "Finding…" : "Find"}
          </Button>
        </form>
        {error && <p className="text-hint text-destructive">{error}</p>}
        {result && (
          <div className="space-y-2">
            <ul aria-label="Free ports" className="divide-y divide-hairline">
              {result.ports.map((port) => (
                <li key={port} className="flex items-center justify-between py-1">
                  <span className="numeric font-mono text-body">{port}</span>
                  <IconAction
                    label={`Copy ${port}`}
                    onClick={() => void copyText(String(port), `Copied ${port}`)}
                  >
                    <Copy />
                  </IconAction>
                </li>
              ))}
            </ul>
            {result.skipped.length > 0 && (
              // Nothing listens on these, so a bind alone would have offered them.
              <p className="text-hint text-muted-foreground">
                Passed over{" "}
                {result.skipped
                  .map((b) => `${b.hostPort} (${b.container}${b.running ? "" : ", stopped"})`)
                  .join(", ")}
                : a stopped container binds its port again when it starts, and Docker publishes a
                running one&apos;s through NAT with no socket.
              </p>
            )}
            {!result.containersChecked && (
              <p className="text-hint text-muted-foreground">
                Docker did not answer, so ports kept by stopped containers were not avoided.
              </p>
            )}
            <p className="text-hint text-muted-foreground">
              Free when checked, not reserved: another program can take one before it is used.
            </p>
          </div>
        )}
      </PopoverContent>
    </Popover>
  )
}

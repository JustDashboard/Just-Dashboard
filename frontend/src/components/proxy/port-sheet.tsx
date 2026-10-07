"use client"

import { useId, useState } from "react"
import Link from "next/link"
import { Inspect, NetworkDevice, Router } from "@/components/icons"
import { errorMessage, post } from "@/lib/api"
import { plural, timestamp } from "@/lib/format"
import type { PortIdentification } from "@/lib/types"
import { Field, FormSection } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { ProductLogo, portProduct } from "@/components/product-logo"
import {
  ownerLabel,
  ownerLinks,
  ownerTitle,
  socketAddresses,
  socketClients,
  socketHolders,
  type Socket,
} from "@/components/proxy/ports"
import { FirewallVerdict } from "@/components/proxy/ports-firewall"
import { formatEndpoint } from "@/components/proxy/ports-list"
import { proxyWords } from "@/components/proxy/ports-proxy"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** The backend sends a socket's busiest peers, at most this many. */
const CLIENT_PEERS_SENT = 5

const MANAGER_WORDS: Record<NonNullable<Socket["manager"]>, string> = {
  systemd: "systemd unit",
  container: "Container",
  session: "Login session",
  pm2: "PM2 app",
  kernel: "Kernel thread",
  unmanaged: "Nothing supervises it",
}

/**
 * One socket in full, opened from its row at `?socket=tcp:0.0.0.0:443`: the
 * whole command a row cuts, every process holding the socket rather than the
 * one a row names, the chain from the process up to whoever supervises it,
 * where it answers and what the firewall and the proxy do with it, who is
 * connected now, and — for an admin — what the socket says when asked.
 *
 * `socket` is absent when the address named one that is no longer listening:
 * the sheet says so rather than closing on a link someone followed.
 */
export function PortSheet({
  param,
  socket,
  reach,
  actions,
  admin,
  onClose,
}: {
  param: string | null
  socket: Socket | undefined
  /** The row's own reach reading, so the sheet words it as the list does. */
  reach: React.ReactNode
  actions: React.ReactNode
  admin: boolean
  onClose: () => void
}) {
  const open = param !== null
  const endpoint = socket ? formatEndpoint(socket.address, socket.port) : (param ?? "")
  return (
    <SidePanel
      open={open}
      onOpenChange={(next) => !next && onClose()}
      width="md"
      initialFocus="body"
      title={
        socket ? (
          <>
            <ProductLogo id={portProduct(socket.port)} size="sm" fallback={Router} />
            <span className="numeric font-mono">{socket.port}</span>
            <span className="text-hint text-muted-foreground uppercase">{socket.protocol}</span>
            <span className="truncate">{ownerTitle(socket)}</span>
            {socket.self && <Tag>This dashboard</Tag>}
          </>
        ) : (
          "Socket"
        )
      }
      description={`The listening socket ${endpoint}`}
      actions={socket ? actions : undefined}
    >
      {socket ? (
        // Keyed on the socket so an identification never shows under another.
        <SocketDetail key={endpoint} socket={socket} reach={reach} admin={admin} />
      ) : (
        <EmptyState
          icon={Router}
          title="Nothing listens there now"
          description={`No socket in the last listing matches ${param ?? ""}.`}
        />
      )}
    </SidePanel>
  )
}

function SocketDetail({
  socket,
  reach,
  admin,
}: {
  socket: Socket
  reach: React.ReactNode
  admin: boolean
}) {
  const nat = socket.source === "docker-nat"
  const holders = socketHolders(socket)
  const clients = socketClients(socket)
  const label = ownerLabel(socket)
  const links = ownerLinks(socket)
  const proxy = proxyWords(socket)
  return (
    <div className="space-y-8">
      <FormSection title="Process">
        {nat ? (
          <p className="text-body text-muted-foreground">
            Forwarded by Docker&apos;s NAT rules; no process listens.
          </p>
        ) : (
          <>
            <pre className="font-mono text-xs break-all whitespace-pre-wrap">
              {socket.cmdline || "No command reported"}
            </pre>
            <DetailList>
              <Detail label="User">{socket.user || "unknown"}</Detail>
              <Detail label={holders.length === 1 ? "Process" : "Processes"}>
                {holders.length === 0 ? (
                  "None this account can see"
                ) : (
                  <span className="flex flex-wrap gap-x-2 gap-y-1">
                    {holders.map((pid) => (
                      <Link
                        key={pid}
                        href={`/processes?pid=${pid}`}
                        className="numeric rounded-sm font-mono underline-offset-2 focus-ring hover:underline"
                      >
                        {pid}
                        {pid === socket.pid && holders.length > 1 && " (owner)"}
                      </Link>
                    ))}
                  </span>
                )}
              </Detail>
              {socket.startedAt && <Detail label="Started">{timestamp(socket.startedAt)}</Detail>}
            </DetailList>
          </>
        )}
      </FormSection>

      <FormSection title="Owner">
        <ol className="space-y-1.5 text-xs">
          {!nat && socket.pid > 0 && (
            <li>
              <span className="text-muted-foreground">Process </span>
              {socket.displayName || socket.process}{" "}
              <span className="numeric font-mono text-muted-foreground">PID {socket.pid}</span>
            </li>
          )}
          {!nat && socket.ppid !== undefined && socket.ppid > 0 && (
            <li>
              <span className="text-muted-foreground">started by </span>
              <Link
                href={`/processes?pid=${socket.ppid}`}
                className="numeric rounded-sm font-mono underline-offset-2 focus-ring hover:underline"
              >
                PID {socket.ppid}
              </Link>
            </li>
          )}
          {socket.manager && (
            <li>
              <span className="text-muted-foreground">supervised by </span>
              {MANAGER_WORDS[socket.manager]}
              {socket.managerName && <span className="font-mono"> {socket.managerName}</span>}
            </li>
          )}
          {socket.socketUnit && (
            <li>
              <span className="text-muted-foreground">socket unit </span>
              <span className="font-mono">{socket.socketUnit}</span>
              {socket.activates && (
                <>
                  <span className="text-muted-foreground"> starts </span>
                  <span className="font-mono">{socket.activates}</span>
                </>
              )}
            </li>
          )}
          {label && (
            <li>
              <span className="text-muted-foreground">answers for </span>
              {label}
            </li>
          )}
        </ol>
        {links.length > 0 && (
          <div className="flex flex-wrap gap-2">
            {links.map((link) => (
              <Button key={link.key} asChild variant="outline" size="xs">
                <Link href={link.href}>{link.label}</Link>
              </Button>
            ))}
          </div>
        )}
      </FormSection>

      <FormSection title="Where it answers">
        <DetailList>
          <Detail label={socketAddresses(socket).length > 1 ? "Addresses" : "Address"}>
            <span className="font-mono">{socketAddresses(socket).join(", ")}</span>
          </Detail>
          <Detail label="Reach">{reach}</Detail>
          <Detail label="Firewall">
            <FirewallVerdict socket={socket} />
          </Detail>
          <Detail label="Proxy">{proxy ?? "Nothing in the proxy routes to it"}</Detail>
        </DetailList>
        {(socket.routes ?? []).length > 1 && (
          <ul className="space-y-1 text-xs">
            {(socket.routes ?? []).map((route) => (
              <li key={route.site} className="flex items-center gap-2">
                <span className="font-mono">{route.serverName || route.site}</span>
                {route.tls && <Tag>TLS</Tag>}
              </li>
            ))}
          </ul>
        )}
      </FormSection>

      {clients && <Clients port={socket.port} clients={clients} />}

      {admin && socket.protocol === "tcp" && !nat && <Identify socket={socket} />}
    </div>
  )
}

/** Who is connected now, from the listing's own walk of the socket tables. */
function Clients({
  port,
  clients,
}: {
  port: number
  clients: NonNullable<ReturnType<typeof socketClients>>
}) {
  return (
    <FormSection
      title="Clients"
      actions={
        <Button asChild variant="outline" size="xs">
          <Link href={`/network/connections?q=${port}`}>
            <NetworkDevice />
            Open in Connections
          </Link>
        </Button>
      }
    >
      {clients.count === 0 ? (
        <p className="text-body text-muted-foreground">
          No connection was open when it was listed.
        </p>
      ) : (
        <>
          <p className="text-body">
            <span className="numeric">{plural(clients.count, "connection")}</span>
            {/* Only the busiest five arrive, so a sixth caller is not counted here. */}
            {clients.peers.length < CLIENT_PEERS_SENT && (
              <span className="text-muted-foreground">
                {" "}
                from {plural(clients.peers.length, "address", "addresses")}
              </span>
            )}
          </p>
          <ul aria-label="Top peers" className="divide-y divide-hairline">
            {clients.peers.map((peer) => (
              <li key={peer.address} className="flex items-center justify-between gap-3 py-1.5">
                <Link
                  href={`/network/connections?q=${encodeURIComponent(peer.address)}`}
                  className="truncate rounded-sm font-mono text-xs underline-offset-2 focus-ring hover:underline"
                >
                  {peer.address}
                </Link>
                <span className="numeric shrink-0 text-hint text-muted-foreground">
                  {peer.count}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </FormSection>
  )
}

/**
 * Asks the socket what it is: a banner if it speaks first, else a TLS
 * handshake and an HTTP request. The server name is sent as SNI and Host, for
 * a server that picks its certificate by name or refuses a handshake without
 * one.
 */
function Identify({ socket }: { socket: Socket }) {
  const id = useId()
  const [serverName, setServerName] = useState(
    socket.routes?.find((route) => route.serverName)?.serverName ?? "",
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<PortIdentification | null>(null)

  const identify = async () => {
    setBusy(true)
    setError(null)
    try {
      setResult(
        await post<PortIdentification>("/ports/identify", {
          protocol: socket.protocol,
          address: socket.address,
          port: socket.port,
          serverName: serverName.trim(),
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
    <FormSection title="Identify">
      <form
        className="space-y-3"
        onSubmit={(event) => {
          event.preventDefault()
          void identify()
        }}
      >
        <Field
          label="Server name"
          htmlFor={`${id}-name`}
          hint="Sent as SNI and Host; leave blank to ask by address"
        >
          <Input
            id={`${id}-name`}
            value={serverName}
            onChange={(e) => setServerName(e.target.value.trim())}
            placeholder="app.example.com"
            className="font-mono"
          />
        </Field>
        <Button type="submit" variant="outline" size="sm" disabled={busy}>
          <Inspect />
          {busy ? "Asking…" : "Identify"}
        </Button>
      </form>
      {error && <p className="text-hint text-destructive">{error}</p>}
      {result && <Identification result={result} port={socket.port} />}
    </FormSection>
  )
}

function Identification({ result, port }: { result: PortIdentification; port: number }) {
  if (!result.connected) {
    return (
      <Notice tone="warning" title={`Could not connect to ${result.endpoint}`}>
        <p>{result.error}</p>
      </Notice>
    )
  }
  const { tls, http } = result
  const stepError = (step: "tls" | "http") => result.steps?.find((s) => s.step === step)?.error
  // The TLS report scans port 443 by name, so it is offered only where that
  // is the socket asked and the certificate names a host.
  const reportName = port === 443 ? tls?.names?.find((name) => !/^[\d.:]+$/.test(name)) : undefined
  return (
    <DetailList>
      <Detail label="Asked">
        <span className="font-mono">{result.endpoint}</span>
      </Detail>
      {result.banner !== undefined && (
        <Detail label="Banner">
          <span className="font-mono break-all">{result.banner}</span>
        </Detail>
      )}
      {!result.banner && (
        <Detail label="TLS">
          {tls?.alert ? (
            `Speaks TLS but refused the handshake: ${tls.alert}`
          ) : tls ? (
            <span className="space-y-0.5">
              <span className="block">
                {tls.version}
                {tls.alpn ? ` · ALPN ${tls.alpn}` : " · no ALPN"}
              </span>
              {tls.subject && <span className="block font-mono">{tls.subject}</span>}
              {tls.names && tls.names.length > 0 && (
                <span className="block font-mono break-all text-muted-foreground">
                  {tls.names.join(", ")}
                </span>
              )}
              <span className="block text-muted-foreground">
                {tls.selfSigned ? "Self-signed" : `Issued by ${tls.issuer || "unknown"}`}
                {tls.notAfter && ` · until ${timestamp(tls.notAfter)}`}
              </span>
              {reportName && (
                <Link
                  href={`/proxy/tls?domain=${encodeURIComponent(reportName)}`}
                  className="block rounded-sm underline underline-offset-2 focus-ring"
                >
                  TLS report for {reportName}
                </Link>
              )}
            </span>
          ) : (
            <span className="text-muted-foreground">
              No{stepError("tls") ? ` (${stepError("tls")})` : ""}
            </span>
          )}
        </Detail>
      )}
      {!result.banner && !tls?.alert && (
        <Detail label="HTTP">
          {http ? (
            <>
              <span className="numeric">{http.status}</span>
              {http.reason && ` ${http.reason}`}
              {http.server && (
                <span className="text-muted-foreground"> · Server {http.server}</span>
              )}
              {http.location && (
                <span className="block font-mono break-all text-muted-foreground">
                  to {http.location}
                </span>
              )}
            </>
          ) : (
            <span className="text-muted-foreground">
              No answer{stepError("http") ? ` (${stepError("http")})` : ""}
            </span>
          )}
        </Detail>
      )}
      {!result.banner && !tls && !http && (
        <Detail label="Said">
          <span className="text-muted-foreground">
            Nothing recognisable: no banner, no TLS and no HTTP
          </span>
        </Detail>
      )}
    </DetailList>
  )
}

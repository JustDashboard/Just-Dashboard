"use client"

import { useState } from "react"
import Link from "next/link"
import { Copy, RefreshClockwise, Trash } from "@/components/icons"
import { del, get, post, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { TunnelHostname, TunnelStatus } from "@/lib/proxy/types-tunnel"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FormFact, FormFacts } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  cnameTarget,
  dnsWords,
  sentence,
  tokenFromPaste,
  tokenProblem,
  tunnelCounts,
  tunnelHealth,
  tunnelNameProblem,
} from "@/components/proxy/tunnel-model"

/**
 * Cloudflare Tunnel as this host's ingress (`backend/internal/cftunnel`).
 *
 * cloudflared runs here as a managed container on a network of its own and
 * dials out to Cloudflare's edge; nothing listens on a public address for
 * it, and Cloudflare terminates the visitor's HTTPS. The page connects one
 * — a token pasted from Cloudflare's dashboard, sealed at once, or a tunnel
 * created with the Cloudflare API token saved for DNS challenges — and then
 * reads it: whether the connector holds connections to the edge, in
 * cloudflared's own words when it does not; which deployment domains are
 * routed through it; and whether each name's record points at it.
 */
export function TunnelPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const read = usePoll((signal) => get<TunnelStatus>("/proxy/tunnel", undefined, signal), 30_000)
  // A verb answers with the tunnel as it left it, which is newer than the poll
  // until the poll's next answer.
  const [answer, setAnswer] = useState<{ status: TunnelStatus; at: number }>()
  const status =
    answer && answer.at > (read.lastSuccess ?? 0) ? answer.status : (read.data ?? answer?.status)
  const [syncing, setSyncing] = useState(false)
  const { confirm, dialog } = useConfirm()

  const header = (
    <PageContext
      eyebrow="Proxy"
      title="Cloudflare Tunnel"
      actions={
        status?.connected &&
        admin && (
          <>
            <Button
              size="sm"
              variant="outline"
              pending={syncing}
              onClick={() => {
                setSyncing(true)
                post<TunnelStatus>("/proxy/tunnel/sync", {})
                  .then((next) => setAnswer({ status: next, at: Date.now() }))
                  .catch((error) => notify.error("Could not check the tunnel", error))
                  .finally(() => setSyncing(false))
              }}
            >
              <RefreshClockwise className="size-3.5" />
              Check now
            </Button>
            {can("destructive") && (
              <Button
                size="sm"
                variant="outline"
                className="text-destructive"
                onClick={() =>
                  confirm({
                    title: "Disconnect the tunnel",
                    confirmLabel: "Disconnect",
                    subject: {
                      mark: <ProductLogo id="cloudflare" size="sm" />,
                      name: status.connection?.tunnelName || status.connection?.tunnelId,
                      facts: (
                        <FormFact label="Routes">
                          {plural(status.hostnames.length, "hostname")}
                        </FormFact>
                      ),
                    },
                    description: (
                      <p>
                        The connector container is removed and its token forgotten, so every name
                        routed through the tunnel stops answering. The tunnel, its routes and its
                        records stay at Cloudflare for you to remove there.
                      </p>
                    ),
                    action: async () => {
                      const next = await del<TunnelStatus>("/proxy/tunnel")
                      setAnswer({ status: next, at: Date.now() })
                    },
                  })
                }
              >
                <Trash className="size-3.5" />
                Disconnect
              </Button>
            )}
          </>
        )
      }
    />
  )

  if (!status && read.loading) {
    return (
      <Page>
        {header}
        <LoadingPanel />
      </Page>
    )
  }
  if (!status) {
    return (
      <Page>
        {header}
        <ErrorState
          error={read.error ?? new Error("The tunnel could not be read.")}
          onRetry={read.refresh}
        />
      </Page>
    )
  }

  return (
    <Page className="animate-rise">
      {header}
      {status.connected ? (
        <Connected status={status} />
      ) : (
        <Connect
          status={status}
          admin={admin}
          onConnected={(next) => setAnswer({ status: next, at: Date.now() })}
        />
      )}
      {dialog}
    </Page>
  )
}

/** The tunnel as it is: the connector, the edge, the names and their records. */
function Connected({ status }: { status: TunnelStatus }) {
  const connection = status.connection
  const health = tunnelHealth(status)
  const counts = tunnelCounts(status)
  const name = connection?.tunnelName || connection?.tunnelId || "Tunnel"
  const manual = status.hostnames.some((hostname) => hostname.dns === "manual")
  return (
    <>
      <StatGrid columns={4}>
        <StatTile
          label="Tunnel"
          value={<span className="truncate">{name}</span>}
          hint={connection?.mode === "api" ? "created here" : "token pasted"}
        />
        <StatTile
          label="Edge connections"
          value={status.connector.state === "running" ? status.readyConnections : "—"}
          hint={<Status tone={health.tone} label={health.label} />}
          tone={health.tone === "danger" ? "danger" : "default"}
        />
        <StatTile
          label="Hostnames"
          value={counts.hostnames}
          hint={
            counts.hostnames === 0
              ? "none marked for the tunnel"
              : status.api.available
                ? `${counts.routed} routed · ${counts.records} with a record`
                : "routes to add by hand"
          }
        />
        <StatTile
          label="Origin"
          value={!status.origin ? "—" : status.origin.hostGateway ? "Host nginx" : "Caddy ingress"}
          hint={status.origin?.service}
        />
      </StatGrid>

      {health.tone === "danger" && (
        <Notice tone="danger" title={health.label} className="animate-rise">
          {status.error ? (
            <>
              cloudflared said: <span className="font-mono break-words">{status.error}</span>
            </>
          ) : (
            "Nothing routed through the tunnel is reachable until the connector holds a connection."
          )}
        </Notice>
      )}
      {status.syncError && (
        <Notice tone="warning" title="The routes could not be set" className="animate-rise">
          {sentence(status.syncError)}
        </Notice>
      )}

      <Panel plain>
        <PanelHeader
          title="Hostnames"
          actions={
            status.syncedAt && (
              <span className="text-hint text-muted-foreground">
                Checked {relativeTime(status.syncedAt)}
              </span>
            )
          }
        />
        <PanelBody flush>
          {status.hostnames.length === 0 ? (
            <EmptyNote>
              No deployment is served through the tunnel yet. Turn on{" "}
              <span className="font-medium text-foreground">Cloudflare Tunnel</span> on a domain in
              a project&apos;s Domains settings, and its next release routes it here.
            </EmptyNote>
          ) : (
            <ul className="divide-y divide-hairline">
              {status.hostnames.map((hostname) => (
                <HostnameRow
                  key={hostname.hostname}
                  hostname={hostname}
                  tunnelId={connection?.tunnelId ?? ""}
                />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      {manual && status.routes && status.routes.length > 0 && (
        <Panel plain>
          <PanelHeader title="Routes to add in Cloudflare" />
          <PanelBody className="space-y-3">
            <p className="max-w-3xl text-hint leading-relaxed text-muted-foreground">
              {status.api.reason
                ? sentence(status.api.reason)
                : "No Cloudflare credentials are saved here."}{" "}
              Add each as a public hostname of the tunnel in Cloudflare&apos;s dashboard, with this
              service, and a proxied CNAME to{" "}
              <span className="font-mono">{cnameTarget(connection?.tunnelId ?? "")}</span>.
            </p>
            <Well className="py-2 font-mono break-all whitespace-pre-wrap">
              {status.routes
                .map((rule) => `${rule.hostname ?? "(anything else)"}  →  ${rule.service}`)
                .join("\n")}
            </Well>
          </PanelBody>
        </Panel>
      )}

      <Panel plain>
        <PanelHeader title="Connector" />
        <PanelBody>
          <FormFacts className="flex flex-wrap gap-x-6 gap-y-2 text-xs text-muted-foreground">
            <FormFact label="Container" mono>
              just-dashboard-cloudflared
            </FormFact>
            <FormFact label="State">{status.connector.state}</FormFact>
            <FormFact label="cloudflared" mono>
              {status.connector.version}
            </FormFact>
            {status.connector.address && (
              <FormFact label="Address" mono>
                {status.connector.address}
              </FormFact>
            )}
            {status.connector.startedAt && (
              <FormFact label="Started">{relativeTime(status.connector.startedAt)}</FormFact>
            )}
            {(status.connector.restartCount ?? 0) > 0 && (
              <FormFact label="Restarts">{status.connector.restartCount}</FormFact>
            )}
            <FormFact label="Published ports">none</FormFact>
          </FormFacts>
          <p className="mt-3 max-w-3xl text-hint leading-relaxed text-muted-foreground">
            It dials out to Cloudflare, so nothing on this host listens for it, and it hands each
            request to {status.origin?.proxy ?? "the proxy"} over plain HTTP on a private network.
            Cloudflare terminates HTTPS, so no certificate is issued here for a tunnel&apos;s names.
          </p>
        </PanelBody>
      </Panel>
    </>
  )
}

function HostnameRow({ hostname, tunnelId }: { hostname: TunnelHostname; tunnelId: string }) {
  const record = dnsWords(hostname)
  return (
    <li className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 py-3">
      <div className="min-w-0 flex-1 space-y-1">
        <a
          href={`https://${hostname.hostname}/`}
          target="_blank"
          rel="noreferrer"
          className="block truncate rounded-sm font-mono text-sm font-medium focus-ring hover:underline"
        >
          {hostname.hostname}
        </a>
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          {hostname.project ? (
            <Link
              href={`/deploy/${hostname.projectId}/settings/domains`}
              className="text-muted-foreground hover:text-foreground hover:underline"
            >
              {hostname.project}
            </Link>
          ) : (
            <span className="text-muted-foreground">going live</span>
          )}
          <Status
            tone={hostname.routed ? "running" : "unknown"}
            label={hostname.routed ? "Routed" : "Not routed"}
          />
          <Status tone={record.tone} label={record.label} />
        </div>
        {hostname.error && hostname.dns === "error" && (
          <p className="text-hint text-destructive">{hostname.error}</p>
        )}
      </div>
      {hostname.dns === "manual" && tunnelId && (
        <IconAction
          label={`Copy the record for ${hostname.hostname}`}
          onClick={() =>
            void copyText(`${hostname.hostname}. CNAME ${cnameTarget(tunnelId)}`, "Record copied")
          }
        >
          <Copy />
        </IconAction>
      )}
    </li>
  )
}

/** No tunnel yet: paste a token, or create one with the saved API token. */
function Connect({
  status,
  admin,
  onConnected,
}: {
  status: TunnelStatus
  admin: boolean
  onConnected: (status: TunnelStatus) => void
}) {
  const [token, setToken] = useState("")
  const [tokenError, setTokenError] = useState<string>()
  const [name, setName] = useState("")
  const [nameError, setNameError] = useState<string>()
  const [account, setAccount] = useState("")
  const [busy, setBusy] = useState<"token" | "create">()

  const connect = async () => {
    const problem = tokenProblem(token)
    setTokenError(problem)
    if (problem) return
    setBusy("token")
    try {
      const next = await put<TunnelStatus>("/proxy/tunnel", { token: tokenFromPaste(token) })
      setToken("")
      notify.success("Tunnel connected")
      onConnected(next)
    } catch (error) {
      notify.error("Could not connect the tunnel", error)
    } finally {
      setBusy(undefined)
    }
  }

  const create = async () => {
    const problem = tunnelNameProblem(name)
    setNameError(problem)
    if (problem) return
    setBusy("create")
    try {
      const next = await post<TunnelStatus>("/proxy/tunnel/create", {
        name: name.trim(),
        accountId: account.trim() || undefined,
      })
      notify.success(`Tunnel ${name.trim()} created`)
      onConnected(next)
    } catch (error) {
      notify.error("Could not create the tunnel", error)
    } finally {
      setBusy(undefined)
    }
  }

  return (
    <>
      <div className="flex min-w-0 items-start gap-3">
        <ProductLogo id="cloudflare" />
        <p className="max-w-3xl text-body leading-relaxed text-muted-foreground">
          Serve deployments through Cloudflare without opening a port: cloudflared runs here as a
          container that dials out to Cloudflare, Cloudflare terminates HTTPS, and requests reach
          {status.origin ? ` ${status.origin.proxy}` : " this host's proxy"} over a private network.
          A domain is then routed through it from its project&apos;s Domains settings.
        </p>
      </div>
      {!admin && (
        <Notice title="Only an administrator connects a tunnel">
          A tunnel token, or the Cloudflare API token spent on creating one, is an
          administrator&apos;s credential.
        </Notice>
      )}
      <div className="grid min-w-0 gap-8 lg:grid-cols-2">
        <Panel plain>
          <PanelHeader title="Paste a tunnel token" />
          <PanelBody className="space-y-4">
            <Field
              label="Tunnel token"
              htmlFor="tunnel-token"
              info="In Cloudflare's dashboard: Zero Trust › Networks › Tunnels, create a cloudflared tunnel, and copy its install command. The token is sealed here and never shown again."
              hint="The token, or the whole install command it is in."
              error={tokenError}
            >
              <Input
                id="tunnel-token"
                type="password"
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
                value={token}
                disabled={!admin}
                onChange={(event) => {
                  setToken(event.target.value)
                  setTokenError(undefined)
                }}
              />
            </Field>
            <p className="text-hint leading-relaxed text-muted-foreground">
              {status.api.available
                ? "With the saved Cloudflare credentials, the routes and records are set here."
                : "Without saved Cloudflare credentials, you add each name's route and record in Cloudflare's dashboard; this page lists them."}
            </p>
            <Button
              onClick={() => void connect()}
              pending={busy === "token"}
              disabled={!admin || !token.trim() || Boolean(busy)}
            >
              Connect
            </Button>
          </PanelBody>
        </Panel>
        <Panel plain>
          <PanelHeader title="Create one with the API" />
          <PanelBody className="space-y-4">
            {!status.api.available && (
              <p className="text-hint leading-relaxed text-muted-foreground">
                {sentence(status.api.reason ?? "No Cloudflare credentials are saved")}{" "}
                <Link href="/proxy/certificates" className="text-foreground underline">
                  Open Certificates
                </Link>
              </p>
            )}
            <Field label="Tunnel name" htmlFor="tunnel-name" error={nameError}>
              <Input
                id="tunnel-name"
                value={name}
                placeholder="production"
                autoComplete="off"
                disabled={!admin || !status.api.available}
                onChange={(event) => {
                  setName(event.target.value)
                  setNameError(undefined)
                }}
              />
            </Field>
            <Field
              label="Account ID"
              htmlFor="tunnel-account"
              hint="Only when the saved token reaches more than one account."
            >
              <Input
                id="tunnel-account"
                value={account}
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
                disabled={!admin || !status.api.available}
                onChange={(event) => setAccount(event.target.value)}
              />
            </Field>
            <Button
              onClick={() => void create()}
              pending={busy === "create"}
              disabled={!admin || !status.api.available || !name.trim() || Boolean(busy)}
            >
              Create tunnel
            </Button>
          </PanelBody>
        </Panel>
      </div>
    </>
  )
}

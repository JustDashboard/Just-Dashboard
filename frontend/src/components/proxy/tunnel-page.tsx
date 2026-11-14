"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import Link from "next/link"
import { ArrowUpRight, Copy, Globe, Key, RefreshClockwise, Trash } from "@/components/icons"
import { del, get, post, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { duration, plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { TunnelHostname, TunnelStatus } from "@/lib/proxy/types-tunnel"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormFact } from "@/components/form"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { Segments } from "@/components/deploy/settings/segments"
import { useNow } from "@/components/deploy/vocabulary"
import { EmptyMark, OverviewEmpty } from "@/components/proxy/overview-empty"
import {
  cnameTarget,
  connectorWords,
  defaultSetup,
  dnsWords,
  sentence,
  tokenFromPaste,
  tokenProblem,
  tunnelCounts,
  tunnelHealth,
  tunnelLines,
  tunnelNameProblem,
  type TunnelLine,
} from "@/components/proxy/tunnel-model"

/** Hostnames drawn in the picture; the rest are counted, and every one is in the list under it. */
const PICTURED = 4

/**
 * Cloudflare Tunnel as this host's ingress (`backend/internal/cftunnel`).
 *
 * cloudflared runs here as a managed container on a network of its own and
 * dials out to Cloudflare's edge; nothing listens on a public address for
 * it, and Cloudflare terminates the visitor's HTTPS.
 *
 * Connected, the page opens on the tunnel — Cloudflare's mark, its name, its
 * edge connections as a state, the connector — with Check now as the line's
 * command, then the picture of the way a visitor arrives: each hostname into
 * Cloudflare's edge, through the connector, to this server's proxy, the
 * lines moving where both ends are up. The hostnames follow as one-line
 * cards with their route and record.
 *
 * Not connected, the same picture is drawn dashed, as what will be wired,
 * over one sentence, a choice between pasting a token and creating one with
 * the saved API token, and the one form that choice asks for.
 *
 * It opened on a paragraph and two forms side by side, and connected, on
 * four tiles over three panels.
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
  const settle = (next: TunnelStatus) => setAnswer({ status: next, at: Date.now() })

  // No way back above the line: the rail lists the section's pages, and
  // none of the others draws one.
  const header = <PageContext title="Tunnel" />

  if (!status) {
    return (
      <Page>
        {header}
        <TunnelIdentityShell
          title="Cloudflare Tunnel"
          state={
            read.loading || !read.error ? (
              <Status tone="unknown" label="Reading" />
            ) : (
              <Status tone="danger" label="Not read" />
            )
          }
          facts={read.loading ? <TextShimmer>Reading the tunnel…</TextShimmer> : undefined}
        />
        {read.loading || !read.error ? (
          <LoadingRows rows={4} />
        ) : (
          <ErrorState error={read.error} onRetry={read.refresh} />
        )}
      </Page>
    )
  }

  return (
    <Page className="animate-rise">
      {header}
      {status.connected ? (
        <Connected status={status} admin={admin} onAnswer={settle} />
      ) : (
        <Setup status={status} admin={admin} onConnected={settle} />
      )}
    </Page>
  )
}

/**
 * The identity line's frame, laid out as a project's header: Cloudflare's
 * mark, a title with the tunnel's state beside it, its facts under them, one
 * command at the end and the rest behind `⋯` — loading, connected and not.
 */
function TunnelIdentityShell({
  title,
  state,
  facts,
  command,
  menu,
}: {
  title: React.ReactNode
  state: React.ReactNode
  facts?: React.ReactNode
  command?: React.ReactNode
  menu?: React.ReactNode
}) {
  return (
    <HostIdentity
      mark="cloudflare"
      title={title}
      state={state}
      facts={facts ?? <span>&nbsp;</span>}
      command={command}
      menu={menu}
    />
  )
}

/** The tunnel as it is: who it is, the way a visitor arrives, the names and their records. */
function Connected({
  status,
  admin,
  onAnswer,
}: {
  status: TunnelStatus
  admin: boolean
  onAnswer: (status: TunnelStatus) => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [syncing, setSyncing] = useState(false)
  const now = useNow(15_000)
  const connection = status.connection
  const connector = status.connector
  const health = tunnelHealth(status)
  const counts = tunnelCounts(status)
  const name = connection?.tunnelName || connection?.tunnelId || "Tunnel"
  const tunnelId = connection?.tunnelId ?? ""
  const manual = status.hostnames.some((hostname) => hostname.dns === "manual")
  const checked = status.syncedAt ?? status.checkedAt
  const started = connector.startedAt ? Date.parse(connector.startedAt) : NaN
  const up = Number.isNaN(started) ? 0 : Math.max(0, (now - started) / 1000)

  const check = () => {
    setSyncing(true)
    post<TunnelStatus>("/proxy/tunnel/sync", {})
      .then(onAnswer)
      .catch((error) => notify.error("Could not check the tunnel", error))
      .finally(() => setSyncing(false))
  }

  const verbs: Verb[] = []
  if (tunnelId) {
    verbs.push({
      key: "copy",
      label: "Copy tunnel ID",
      icon: Copy,
      run: () => void copyText(tunnelId, "Tunnel ID copied"),
    })
  }
  if (admin && can("destructive")) {
    verbs.push({
      key: "disconnect",
      label: "Disconnect",
      icon: Trash,
      danger: true,
      run: () =>
        confirm({
          title: "Disconnect the tunnel",
          confirmLabel: "Disconnect",
          subject: {
            mark: <ProductLogo id="cloudflare" size="sm" />,
            name: connection?.tunnelName || connection?.tunnelId,
            facts: (
              <FormFact label="Routes">{plural(status.hostnames.length, "hostname")}</FormFact>
            ),
          },
          description: (
            <p>
              The connector container is removed and its token forgotten, so every name routed
              through the tunnel stops answering. The tunnel, its routes and its records stay at
              Cloudflare for you to remove there.
            </p>
          ),
          action: async () => {
            onAnswer(await del<TunnelStatus>("/proxy/tunnel"))
          },
        }),
    })
  }

  return (
    <>
      <TunnelIdentityShell
        title={name}
        state={
          <Status
            tone={health.tone}
            label={
              health.label === "Connected"
                ? plural(status.readyConnections, "edge connection")
                : health.label
            }
          />
        }
        facts={
          <>
            <span>{connection?.mode === "api" ? "created here" : "token pasted"}</span>
            <FactDot />
            <HostFact product="docker">
              <span
                title={[
                  "just-dashboard-cloudflared",
                  connector.address,
                  (connector.restartCount ?? 0) > 0 &&
                    plural(connector.restartCount ?? 0, "restart"),
                ]
                  .filter(Boolean)
                  .join(" · ")}
              >
                cloudflared <span className="font-mono text-[0.9em]">{connector.version}</span>
                {connector.state === "running" && !connector.foreign
                  ? up > 60
                    ? `, up ${duration(up).split(" ")[0]}`
                    : ""
                  : `, ${connectorWords(connector)}`}
              </span>
            </HostFact>
            <FactDot />
            <span>no published port</span>
            {checked && (
              <>
                <FactDot />
                {/* The clock above re-renders the line, so "checked 2m ago"
                    moves between polls rather than holding its first answer. */}
                <span>checked {relativeTime(checked)}</span>
              </>
            )}
          </>
        }
        command={
          admin && (
            <Button size="sm" onClick={check} pending={syncing} disabled={syncing}>
              <RefreshClockwise />
              {syncing ? "Checking…" : "Check now"}
            </Button>
          )
        }
        menu={verbs.length > 0 && <VerbMenu verbs={verbs} label="More tunnel actions" />}
      />

      {(health.tone === "danger" || status.error) && (
        <Notice
          tone="danger"
          title={health.tone === "danger" ? health.label : "cloudflared reports a problem"}
          className="animate-rise"
        >
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

      <TunnelPicture status={status} />

      <Panel plain aria-label="Hostnames">
        <PanelHeader
          title="Hostnames"
          actions={
            <span className="numeric text-hint text-muted-foreground">{counts.hostnames}</span>
          }
        />
        <PanelBody className="pt-4">
          {status.hostnames.length === 0 ? (
            <OverviewEmpty
              mark={<EmptyMark fallback={Globe} />}
              title="No deployment is served through the tunnel yet"
              action={
                <Button asChild size="sm" variant="outline">
                  <Link href="/deploy">Deployments</Link>
                </Button>
              }
            >
              Turn on Cloudflare Tunnel for a domain in a project&apos;s Domains settings.
            </OverviewEmpty>
          ) : (
            <ChoiceList aria-label="Hostnames through the tunnel">
              {status.hostnames.map((hostname) => (
                <HostnameRow key={hostname.hostname} hostname={hostname} tunnelId={tunnelId} />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      {manual && status.routes && status.routes.length > 0 && (
        <Panel plain aria-label="Routes to add in Cloudflare">
          <PanelHeader title="Routes to add in Cloudflare" />
          <PanelBody className="space-y-3 pt-4">
            <p className="max-w-3xl text-hint leading-relaxed text-muted-foreground">
              {status.api.reason
                ? sentence(status.api.reason)
                : "No Cloudflare credentials are saved here."}{" "}
              Add each as a public hostname of the tunnel, with a proxied CNAME to{" "}
              <span className="font-mono text-foreground">{cnameTarget(tunnelId)}</span>.
            </p>
            <Well className="py-2 font-mono break-all whitespace-pre-wrap">
              {status.routes
                .map((rule) => `${rule.hostname ?? "(anything else)"}  →  ${rule.service}`)
                .join("\n")}
            </Well>
          </PanelBody>
        </Panel>
      )}
      {dialog}
    </>
  )
}

/**
 * One hostname routed through the tunnel, at one height whatever its state:
 * the name, its project (or why its record was refused, in the project's
 * place), then whether the tunnel routes it and what its record says, in
 * two aligned columns. It opens the project's Domains settings, where it is
 * turned on and off.
 */
function HostnameRow({ hostname, tunnelId }: { hostname: TunnelHostname; tunnelId: string }) {
  const record = dnsWords(hostname)
  const refused = hostname.dns === "error" && hostname.error
  const verbs: Verb[] = [
    {
      key: "visit",
      label: `Visit ${hostname.hostname}`,
      icon: ArrowUpRight,
      run: () => window.open(`https://${hostname.hostname}/`, "_blank", "noopener,noreferrer"),
    },
  ]
  if (hostname.dns === "manual" && tunnelId) {
    verbs.push({
      key: "record",
      label: "Copy the record",
      icon: Copy,
      run: () =>
        void copyText(`${hostname.hostname}. CNAME ${cnameTarget(tunnelId)}`, "Record copied"),
    })
  }
  // A ChoiceRow is its own list item.
  return (
    <ChoiceRow
      href={hostname.projectId ? `/deploy/${hostname.projectId}/settings/domains` : undefined}
      disabled={!hostname.projectId}
      verb={`${hostname.hostname} in ${hostname.project || "its project"}'s Domains`}
      className="min-h-0 gap-3 px-3 py-2.5"
      leading={
        <WireMark tone={record.tone === "danger" ? "danger" : "logo"} size="sm">
          <Globe aria-hidden />
        </WireMark>
      }
      title={<span className="font-mono">{hostname.hostname}</span>}
      description={
        refused ? (
          <span className="text-destructive" title={hostname.error}>
            {hostname.error}
          </span>
        ) : (
          hostname.project || "going live"
        )
      }
      trailing={
        <span className="hidden items-center gap-4 sm:flex">
          <span className="w-24">
            <Status
              tone={hostname.routed ? "running" : "unknown"}
              label={hostname.routed ? "Routed" : "Not routed"}
            />
          </span>
          <span className="w-48 truncate">
            <Status tone={record.tone} label={record.label} />
          </span>
        </span>
      }
      actions={<VerbMenu verbs={verbs} label={`More for ${hostname.hostname}`} />}
    >
      {/* A phone has no width for the two columns, so they are the row's
            second line there — on every row, so every row is one height. */}
      <span className="flex min-w-0 items-center gap-4 pl-12 text-hint sm:hidden">
        <Status
          tone={hostname.routed ? "running" : "unknown"}
          label={hostname.routed ? "Routed" : "Not routed"}
        />
        <span className="min-w-0 truncate">
          <Status tone={record.tone} label={record.label} />
        </span>
      </span>
    </ChoiceRow>
  )
}

/**
 * The way a visitor arrives: each hostname into Cloudflare's edge, through
 * the connector dialled out from here, to this server's proxy. Connected,
 * a line moves where both its ends are up, and is red and dashed where one
 * is not; not connected, the same picture is drawn dashed, as what will be
 * wired. Wide, the four stand in a row with the hostnames stacked at the
 * start; narrow, in one column.
 */
function TunnelPicture({ status }: { status: TunnelStatus }) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const container = useRef<HTMLDivElement>(null)
  const edge = useRef<HTMLDivElement>(null)
  const connector = useRef<HTMLDivElement>(null)
  const server = useRef<HTMLDivElement>(null)
  const names = useMemo(
    () => Array.from({ length: PICTURED }, () => createRef<HTMLDivElement>()),
    [],
  )
  const lines = tunnelLines(status)
  const connected = status.connected
  const hostnames = status.hostnames.slice(0, wide ? PICTURED : 1)
  const more = status.hostnames.length - hostnames.length
  const origin = status.origin
  const running = status.connector.state === "running" && !status.connector.foreign

  const hostnameNodes =
    hostnames.length === 0 ? (
      <WireNode
        nodeRef={names[0]}
        align={wide ? "end" : "start"}
        mark={<WirePlaceholder size="md" fallback={Globe} />}
        title={<span className="text-muted-foreground">Deployment domains</span>}
        hint="turned on in a project's Domains"
      />
    ) : (
      hostnames.map((hostname, index) => {
        const record = dnsWords(hostname)
        return (
          <WireNode
            key={hostname.hostname}
            nodeRef={names[index]}
            align={wide ? "end" : "start"}
            mark={
              <WireMark
                tone={
                  record.tone === "danger"
                    ? "danger"
                    : record.tone === "warning"
                      ? "warning"
                      : "logo"
                }
                size="md"
              >
                <Globe aria-hidden />
              </WireMark>
            }
            title={
              <span className="block max-w-56 truncate font-mono" title={hostname.hostname}>
                {hostname.hostname}
              </span>
            }
            hint={
              !wide && more > 0
                ? `and ${plural(more, "more hostname")}`
                : hostname.routed
                  ? record.label
                  : "Not routed"
            }
          />
        )
      })
    )

  const edgeNode = (
    <WireNode
      nodeRef={edge}
      align={wide ? "center" : "start"}
      eyebrow="Cloudflare"
      mark={
        <WireMark tone="logo" size="lg">
          <ProductGlyph id="cloudflare" />
        </WireMark>
      }
      title="Edge"
      hint={
        connected
          ? status.ready
            ? plural(status.readyConnections, "connection")
            : "no connection"
          : "terminates HTTPS"
      }
    />
  )
  const connectorNode = (
    <WireNode
      nodeRef={connector}
      align={wide ? "center" : "start"}
      eyebrow="Connector"
      mark={
        connected ? (
          <WireMark tone={running ? "logo" : "danger"} size="lg">
            <ProductGlyph id="docker" />
          </WireMark>
        ) : (
          <WirePlaceholder size="lg" product="docker" />
        )
      }
      title="cloudflared"
      hint={
        connected
          ? running
            ? (status.connector.address ?? "running")
            : connectorWords(status.connector)
          : "dials out from here"
      }
    />
  )
  const serverNode = (
    <WireNode
      nodeRef={server}
      align="start"
      eyebrow="This server"
      mark={<WireHost size={wide ? "lg" : "md"} />}
      title={!origin ? "this server's proxy" : origin.hostGateway ? "nginx" : "Caddy ingress"}
      hint={origin && <span className="font-mono">{origin.service}</span>}
    />
  )

  const count = Math.max(1, hostnames.length)
  const beams = (
    <>
      {Array.from({ length: count }, (_, index) => (
        <TunnelBeam
          key={`name-${index}`}
          containerRef={container}
          fromRef={names[index]}
          toRef={edge}
          line={hostnames[index] ? lines.hostname(hostnames[index]) : lines.edge}
          wide={wide}
          delay={index * 0.3}
        />
      ))}
      <TunnelBeam
        containerRef={container}
        fromRef={edge}
        toRef={connector}
        line={lines.edge}
        wide={wide}
        delay={0.4}
      />
      <TunnelBeam
        containerRef={container}
        fromRef={connector}
        toRef={server}
        line={lines.origin}
        wide={wide}
        delay={0.8}
      />
    </>
  )

  return (
    <div
      ref={container}
      role="group"
      aria-label={connected ? "The way a visitor arrives" : "What a tunnel wires"}
      className="relative mx-auto w-full max-w-5xl py-4 lg:py-6"
    >
      {beams}
      {wide ? (
        <div className="grid grid-cols-[minmax(0,1.2fr)_minmax(3rem,0.5fr)_auto_minmax(3rem,0.6fr)_auto_minmax(3rem,0.6fr)_minmax(0,1fr)] items-center pb-10">
          <div className="flex min-w-0 flex-col items-end gap-4">
            {hostnameNodes}
            {more > 0 && (
              <p className="pr-14 text-hint text-muted-foreground">
                and {plural(more, "more hostname")} in the list
              </p>
            )}
          </div>
          <div aria-hidden />
          {edgeNode}
          <div aria-hidden />
          {connectorNode}
          <div aria-hidden />
          {serverNode}
        </div>
      ) : (
        <div className={cn("flex flex-col gap-7")}>
          {hostnameNodes}
          {edgeNode}
          {connectorNode}
          {serverNode}
        </div>
      )}
    </div>
  )
}

function TunnelBeam({
  containerRef,
  fromRef,
  toRef,
  line,
  wide,
  delay,
}: {
  containerRef: RefObject<HTMLDivElement | null>
  fromRef: RefObject<HTMLDivElement | null>
  toRef: RefObject<HTMLDivElement | null>
  line: TunnelLine
  wide: boolean
  delay: number
}) {
  return (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={fromRef}
      toRef={toRef}
      shape={wide ? "s" : "arc"}
      still={!line.live}
      dashed={line.dashed}
      tone={line.tone}
      duration={2.6}
      delay={delay}
    />
  )
}

/**
 * No tunnel yet: what one wires, one sentence, a choice of the way in, and
 * the one form it asks for, in the settings column.
 */
function Setup({
  status,
  admin,
  onConnected,
}: {
  status: TunnelStatus
  admin: boolean
  onConnected: (status: TunnelStatus) => void
}) {
  const [way, setWay] = useState<"token" | "api">(() => defaultSetup(status))
  return (
    <>
      <TunnelIdentityShell
        title="Cloudflare Tunnel"
        state={<Status tone="unknown" label="not connected" />}
        facts={
          <>
            <span className={cn(!status.api.available && "text-muted-foreground")}>
              {status.api.available
                ? "Cloudflare credentials saved"
                : "no Cloudflare credentials saved"}
            </span>
            {status.origin && (
              <>
                <FactDot />
                <span>
                  to <span className="font-mono text-[0.9em]">{status.origin.service}</span>
                </span>
              </>
            )}
          </>
        }
      />
      <TunnelPicture status={status} />
      <p className="mx-auto max-w-2xl text-center text-body text-pretty text-muted-foreground">
        cloudflared dials out from this server to Cloudflare, which answers for your domains over
        HTTPS, so no port opens here.
      </p>
      <div className="mx-auto w-full max-w-3xl min-w-0 space-y-6">
        {!admin ? (
          <Notice title="Only an administrator connects a tunnel">
            A tunnel token, or the Cloudflare API token spent on creating one, is an
            administrator&apos;s credential.
          </Notice>
        ) : (
          <>
            <div className="border-b border-hairline pb-4">
              <Segments
                label="How to connect"
                value={way}
                onChange={setWay}
                options={[
                  { value: "token", label: "Paste a token" },
                  { value: "api", label: "Create with the API" },
                ]}
              />
            </div>
            {way === "token" ? (
              <TokenForm status={status} onConnected={onConnected} />
            ) : (
              <CreateForm status={status} onConnected={onConnected} />
            )}
          </>
        )}
      </div>
    </>
  )
}

function TokenForm({
  status,
  onConnected,
}: {
  status: TunnelStatus
  onConnected: (status: TunnelStatus) => void
}) {
  const [token, setToken] = useState("")
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)

  const connect = async () => {
    const problem = tokenProblem(token)
    setError(problem)
    if (problem) return
    setBusy(true)
    try {
      const next = await put<TunnelStatus>("/proxy/tunnel", { token: tokenFromPaste(token) })
      setToken("")
      notify.success("Tunnel connected")
      onConnected(next)
    } catch (err) {
      notify.error("Could not connect the tunnel", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form
      aria-label="Connect with a token"
      className="min-w-0 space-y-5"
      onSubmit={(event) => {
        event.preventDefault()
        void connect()
      }}
    >
      <Field
        label="Tunnel token"
        htmlFor="tunnel-token"
        info={`In Cloudflare's dashboard: Zero Trust › Networks › Tunnels, create a cloudflared tunnel and copy its install command; the whole command is fine. The token is sealed here and never shown again. ${
          status.api.available
            ? "With the saved credentials, its routes and records are set here."
            : "Without saved credentials, you add each name's route and record in Cloudflare; this page lists them."
        }`}
        error={error}
      >
        <Input
          id="tunnel-token"
          type="password"
          autoComplete="off"
          spellCheck={false}
          className="font-mono"
          value={token}
          onChange={(event) => {
            setToken(event.target.value)
            setError(undefined)
          }}
        />
      </Field>
      <Button type="submit" pending={busy} disabled={!token.trim() || busy}>
        Connect
      </Button>
    </form>
  )
}

function CreateForm({
  status,
  onConnected,
}: {
  status: TunnelStatus
  onConnected: (status: TunnelStatus) => void
}) {
  const [name, setName] = useState("")
  const [error, setError] = useState<string>()
  const [account, setAccount] = useState("")
  const [busy, setBusy] = useState(false)

  if (!status.api.available) {
    return (
      <OverviewEmpty
        mark={<EmptyMark product="cloudflare" corner={Key} tone="warning" />}
        title="Creating a tunnel takes a Cloudflare API token"
        action={
          <Button asChild size="sm" variant="outline">
            <Link href="/proxy/certificates">Open Certificates</Link>
          </Button>
        }
      >
        {sentence(status.api.reason ?? "save an API token for the Cloudflare DNS provider")}
      </OverviewEmpty>
    )
  }

  const create = async () => {
    const problem = tunnelNameProblem(name)
    setError(problem)
    if (problem) return
    setBusy(true)
    try {
      const next = await post<TunnelStatus>("/proxy/tunnel/create", {
        name: name.trim(),
        accountId: account.trim() || undefined,
      })
      notify.success(`Tunnel ${name.trim()} created`)
      onConnected(next)
    } catch (err) {
      notify.error("Could not create the tunnel", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form
      aria-label="Create a tunnel"
      className="min-w-0 space-y-5"
      onSubmit={(event) => {
        event.preventDefault()
        void create()
      }}
    >
      <Field label="Tunnel name" htmlFor="tunnel-name" error={error}>
        <Input
          id="tunnel-name"
          value={name}
          placeholder="production"
          autoComplete="off"
          onChange={(event) => {
            setName(event.target.value)
            setError(undefined)
          }}
        />
      </Field>
      <Field
        label="Account ID"
        htmlFor="tunnel-account"
        info="Only when the saved token reaches more than one Cloudflare account."
      >
        <Input
          id="tunnel-account"
          value={account}
          autoComplete="off"
          spellCheck={false}
          className="font-mono"
          onChange={(event) => setAccount(event.target.value)}
        />
      </Field>
      <Button type="submit" pending={busy} disabled={!name.trim() || busy}>
        Create tunnel
      </Button>
    </form>
  )
}

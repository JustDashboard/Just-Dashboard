"use client"

import { useId, useRef, useState } from "react"
import Link from "next/link"
import { Cross, Globe, Router, Servers, Shield, Warning } from "@/components/icons"
import { post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type {
  DbAccess,
  DbAccessChange,
  DbAccessPreview,
  DbAccessRequest,
  DbHostAddress,
} from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { CidrText, cidrKind } from "@/components/config/allowlist"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { Field, FormFact, FormNote, Statement } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Detail, DetailList } from "@/components/page"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { CouldNotRead, FactsSkeleton } from "@/components/database/home/blocks"
import { read, record } from "@/components/database/home/read"
import { EngineGlyph, EngineMark } from "@/components/database/kit"
import {
  EXPOSURE,
  addressWords,
  firewallOutcome,
  firewallWords,
  gatewayOutcome,
  joinWords,
  reachRefusal,
  sameList,
  sourceProblem,
} from "@/components/database/ops/settings-model"
import { useDatabase } from "@/components/database/shell/database-context"

type Reach = DbAccess["exposure"]

/**
 * Where the server can be reached from, and the four answers this page can
 * give it for a container the dashboard owns: this machine only, one of its
 * own addresses, a list of addresses, or anywhere.
 *
 * None of them is a switch. A new binding recreates the container — every
 * session is dropped while it is replaced — and each answer writes or removes
 * firewall rules, so each says what it does before it is taken. The two that
 * take a value open their field under the choices: the address, picked from
 * the ones this machine has, or the list. Those two are confirmed with what
 * the server says it will run, rules and all, because the list is enforced by
 * the gateway table the network module keeps and not by a rule the reader
 * could look for on the Firewall page alone.
 *
 * Where the binding is not the dashboard's to change, the page says whose it
 * is instead of offering a control the server would refuse. Any role reads
 * the summary's word. The addresses, the list and the firewall are an
 * administrator's read, and only an administrator asks for them.
 */
export function ReachabilitySection({ confirm }: { confirm: (request: ConfirmRequest) => void }) {
  const { id, conn, engine, summary, status } = useDatabase()
  const { can } = useAuth()
  const admin = can("system.admin")
  const [changing, setChanging] = useState<Reach>()
  const [reviewing, setReviewing] = useState(false)
  const [editing, setEditing] = useState<"private" | "allowlist">()
  const access = usePoll(
    (signal) =>
      read<DbAccess>(
        `/databases/${id}/access`,
        (answer) => record(answer.firewall) && Array.isArray(answer.publicAddresses),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled: admin },
  )

  const data = access.data
  const exposure = data?.exposure ?? summary?.exposure ?? "unknown"
  const words = EXPOSURE[exposure] ?? EXPOSURE.unknown
  const refusal = data ? reachRefusal(data) : undefined
  const mayChange = admin && can("destructive") && data !== undefined && !refusal
  const port = data?.port ?? (Number(conn.port) || undefined)
  const firewall = data?.firewall
  const rule = firewall?.backend ?? "the firewall"
  // The field that is open: the one asked for, or the one the server is
  // already in, which is where its value is changed.
  const open =
    editing ??
    (data?.exposure === "private" || data?.exposure === "allowlist" ? data.exposure : undefined)
  const container = data?.container ?? "its container"
  const subject = {
    mark: <EngineMark engine={engine} size="sm" />,
    name: conn.name,
    facts: (
      <>
        <FormFact label="Container" mono>
          {container}
        </FormFact>
        <FormFact label="Port" mono>
          {port ?? "—"}
        </FormFact>
      </>
    ),
  }

  /** Sends the change. The recreate can take a minute: the dialog begins it and the section says it is happening. */
  const send = (request: DbAccessRequest, done: string) => {
    void (async () => {
      setChanging(request.exposure)
      try {
        const answer = await put<DbAccessChange>(`/databases/${id}/access`, request)
        const said = [
          gatewayOutcome(answer.gateway, answer.access.port),
          firewallOutcome(answer.firewall, answer.access.port, answer.access.exposure),
        ]
          .filter(Boolean)
          .join(" ")
        if (answer.gatewayError || answer.firewallError) {
          notify.warning(`${conn.name} changed, and not everything followed`, {
            description: [answer.gatewayError, answer.firewallError].filter(Boolean).join(" "),
          })
        } else {
          notify.success(done, { description: said || undefined })
        }
        setEditing(undefined)
      } catch (err) {
        notify.error(`Could not change where ${conn.name} is reachable from`, err)
      } finally {
        setChanging(undefined)
        access.refresh()
        status.refresh()
      }
    })()
    return "reported" as const
  }

  const change = (to: "local" | "public") => {
    if (!data || to === data.exposure) return
    setEditing(undefined)
    // A list on every interface is already the binding Anywhere needs.
    const recreates = to === "public" ? data.exposure !== "allowlist" : true
    const listed = data.exposure === "allowlist"
    confirm({
      title: to === "public" ? `Publish ${conn.name} to anywhere` : `Restrict ${conn.name}`,
      confirmLabel: to === "public" ? "Publish" : "Restrict",
      subject,
      description:
        to === "public" ? (
          <>
            <p>
              {recreates
                ? `Recreates ${container} with port ${port} bound to every interface of this machine${
                    firewall?.active && firewall.editable
                      ? `, and adds a rule to ${rule} that allows port ${port} from anywhere.`
                      : "."
                  }`
                : `The gateway stops refusing the addresses that are not on the list${
                    firewall?.active && firewall.editable
                      ? `, and ${rule}'s rules for the listed addresses give way to one that allows port ${port} from anywhere.`
                      : "."
                  }`}{" "}
              Anything that can reach this machine can then try the password.
            </p>
            <p>
              {recreates
                ? "The data is kept. Every open session is dropped while the container is replaced."
                : "Nothing restarts, and open sessions carry on."}
            </p>
          </>
        ) : (
          <>
            <p>
              Recreates {container} with port {port} bound to this machine only, and removes the
              firewall rules this dashboard wrote for it
              {listed ? " and the gateway's list" : ""}. Anything that reaches it from another
              machine stops reaching it.
            </p>
            <p>The data is kept. Every open session is dropped while the container is replaced.</p>
          </>
        ),
      action: async () =>
        send(
          { exposure: to },
          to === "public"
            ? `${conn.name} is reachable from anywhere`
            : `${conn.name} is reachable from this server only`,
        ),
    })
  }

  /**
   * Asks the server what the change would run, then confirms with that: the
   * gateway's rules, the recreate, and the firewall's plan in its own words.
   */
  const review = async (
    request: Extract<DbAccessRequest, { exposure: "private" | "allowlist" }>,
  ) => {
    if (!data) return
    setReviewing(true)
    let preview: DbAccessPreview
    try {
      preview = await post<DbAccessPreview>(`/databases/${id}/access/preview`, request)
    } catch (err) {
      notify.error("Could not work out what the change would do", err)
      return
    } finally {
      setReviewing(false)
    }
    const listed = request.exposure === "allowlist"
    const rules = preview.steps.flatMap((step) => step.lines ?? [])
    confirm({
      title: listed
        ? `Allow only listed addresses to reach ${conn.name}`
        : `Publish ${conn.name} on one address`,
      confirmLabel: listed ? "Allow these addresses" : "Publish on this address",
      subject,
      description: (
        <>
          <ol className="list-inside list-decimal space-y-1" aria-label="What runs">
            {preview.steps.map((step, index) => (
              <li key={`${step.kind}:${index}`}>{step.description}</li>
            ))}
          </ol>
          {rules.length > 0 && (
            <Statement label="The gateway's rules" sql={rules.join("\n")} placeholder="" />
          )}
          {preview.firewallError && (
            <Notice tone="warning" icon={Warning} title="The firewall will not follow">
              {preview.firewallError}
            </Notice>
          )}
          <p>
            {preview.recreate
              ? "The data is kept. Every open session is dropped while the container is replaced."
              : "Nothing restarts. Sessions already open from other addresses carry on until they close."}
          </p>
        </>
      ),
      action: async () =>
        send(
          request,
          listed
            ? `${conn.name} is reachable from the listed addresses`
            : `${conn.name} is reachable on ${request.address}`,
        ),
    })
  }

  const busy = Boolean(changing) || reviewing

  return (
    <SettingSection
      id="reachability"
      title="Where it can be reached from"
      state={
        <span className="flex">
          {exposure === "public" ? (
            <Status verdict="warning" label={words.word} />
          ) : (
            <Status tone="unknown" label={words.word} />
          )}
        </span>
      }
    >
      <ReachPicture access={data} exposure={exposure} changing={Boolean(changing)} />
      <p className="text-body leading-relaxed text-muted-foreground">
        {changing ? (
          <TextShimmer>Changing where it is reachable from…</TextShimmer>
        ) : (
          words.sentence
        )}
      </p>

      {mayChange && data && (
        <ChoiceGrid columns={2} role="group" aria-label="Where it is reachable from">
          <ChoiceCard
            selected={data.exposure === "local"}
            disabled={busy}
            onClick={() => change("local")}
          >
            <ChoiceMark
              icon={Servers}
              selected={data.exposure === "local"}
              title="This server only"
            />
            <ChoiceCardHint>
              Port {port} on loopback. Programs on this machine and the deployments linked to it
              reach it; nothing else does.
            </ChoiceCardHint>
          </ChoiceCard>
          <ChoiceCard
            selected={data.exposure === "private"}
            disabled={busy || data.addresses.length === 0}
            onClick={() => setEditing("private")}
          >
            <ChoiceMark
              icon={Router}
              selected={data.exposure === "private"}
              title="One address of this machine"
            />
            <ChoiceCardHint>
              {data.addresses.length === 0
                ? "This machine has no tailnet, VPN or local-network address to publish on."
                : `Port ${port} on one address — your tailnet, a VPN, a local network. Only that network reaches it.`}
            </ChoiceCardHint>
          </ChoiceCard>
          <ChoiceCard
            selected={data.exposure === "allowlist"}
            disabled={busy || !data.gateway.available}
            onClick={() => setEditing("allowlist")}
          >
            <ChoiceMark
              icon={Shield}
              selected={data.exposure === "allowlist"}
              title="Allowed addresses"
            />
            <ChoiceCardHint>
              {data.gateway.available
                ? `Port ${port} on every interface, and the gateway refuses every address not on your list.`
                : data.gateway.reason}
            </ChoiceCardHint>
          </ChoiceCard>
          <ChoiceCard
            selected={data.exposure === "public"}
            disabled={busy}
            onClick={() => change("public")}
          >
            <span className="flex items-center gap-2">
              <Globe
                aria-hidden
                className={cn(
                  "size-4 shrink-0",
                  data.exposure === "public" ? "text-warning" : "text-muted-foreground",
                )}
              />
              <ChoiceCardTitle>Anywhere</ChoiceCardTitle>
            </span>
            <ChoiceCardHint>
              Port {port} on every interface
              {firewall?.active && firewall.editable
                ? `, with a rule in ${rule} that lets it in from anywhere.`
                : "."}{" "}
              Only the password stands between it and the internet.
            </ChoiceCardHint>
          </ChoiceCard>
        </ChoiceGrid>
      )}

      {mayChange && data && open === "private" && (
        <OneAddress
          access={data}
          busy={busy}
          onCancel={data.exposure === "private" ? undefined : () => setEditing(undefined)}
          onReview={(address) => void review({ exposure: "private", address })}
        />
      )}
      {mayChange && data && open === "allowlist" && (
        <AllowedAddresses
          // A fresh draft for every saved list, so what the server
          // normalised is what the field starts from again.
          key={data.allow.join(",")}
          access={data}
          busy={busy}
          onCancel={data.exposure === "allowlist" ? undefined : () => setEditing(undefined)}
          onReview={(allow) => void review({ exposure: "allowlist", allow })}
        />
      )}

      {data?.exposure === "public" && (
        <Notice
          tone="warning"
          icon={Warning}
          title="Anything that can reach this machine can try it"
        >
          {firewall?.active && !firewall.open
            ? `${rule} is on and port ${port} is closed in it, so for now only this machine's own networks get through.`
            : "The password is the only thing in the way. Restrict it to this server unless something outside has to connect."}
        </Notice>
      )}
      {data?.exposure === "allowlist" && !data.gateway.enforced && (
        <Notice tone="warning" icon={Warning} title="The list is not in force">
          The gateway table that refuses the other addresses is not loaded on this machine right
          now. The Network section&rsquo;s Protection page says why.
        </Notice>
      )}
      {data && data.exposure !== "allowlist" && data.allow.length > 0 && (
        <Notice
          tone="warning"
          icon={Warning}
          title="A list from an unfinished change is still kept"
        >
          The gateway still refuses every address but {joinWords(data.allow)} on port {port}. Choose
          where it can be reached from again to clear it.
        </Notice>
      )}

      {admin &&
        (data ? (
          <DetailList className="animate-rise gap-y-2.5 border-t border-hairline pt-5">
            {conn.port && (
              <Detail label="Listens on" className="font-mono">
                {conn.host}:{conn.port}
              </Detail>
            )}
            {data.container && (
              <Detail label="Container" className="font-mono wrap-anywhere">
                <Link
                  href={`/docker/containers/${encodeURIComponent(data.container)}`}
                  className="rounded-sm underline focus-ring"
                >
                  {data.container}
                </Link>
                {data.composeProject ? ` · compose project ${data.composeProject}` : ""}
              </Detail>
            )}
            {data.exposure === "private" && data.address && (
              <Detail label="Published on" className="font-mono wrap-anywhere">
                {data.address}
              </Detail>
            )}
            {data.publicAddresses.length > 0 && data.exposure !== "remote" && (
              <Detail label="This machine" className="font-mono wrap-anywhere">
                {data.publicAddresses.join(", ")}
              </Detail>
            )}
            {data.exposure === "allowlist" && (
              <Detail label="Gateway">
                {data.gateway.enforced
                  ? `Refuses every address not on the list${data.gateway.refused > 0 ? ` — ${data.gateway.refused.toLocaleString()} attempts so far` : ""}.`
                  : "Not loaded: the list is not in force."}
              </Detail>
            )}
            {data.exposure !== "remote" && <Detail label="Firewall">{firewallWords(data)}</Detail>}
          </DetailList>
        ) : access.error ? (
          <CouldNotRead
            what="where it is reachable from"
            error={access.error}
            onRetry={access.refresh}
          />
        ) : (
          <FactsSkeleton rows={3} />
        ))}

      {admin && refusal && (
        <FormNote data-slot="reach-refusal">
          <span className="font-medium text-foreground">Not changed from here.</span> {refusal}
        </FormNote>
      )}
      {admin && data && !refusal && !can("destructive") && (
        <FormNote>Changing it recreates the container, which your role may not do.</FormNote>
      )}
      {!admin && (
        <FormNote>
          The addresses and the firewall are an administrator&rsquo;s to read and to change.
        </FormNote>
      )}
    </SettingSection>
  )
}

/** A choice's glyph and title, lit when it is the one in force. */
function ChoiceMark({
  icon: Icon,
  selected,
  title,
}: {
  icon: typeof Servers
  selected: boolean
  title: string
}) {
  return (
    <span className="flex items-center gap-2">
      <Icon
        aria-hidden
        className={cn("size-4 shrink-0", selected ? "text-brand" : "text-muted-foreground")}
      />
      <ChoiceCardTitle>{title}</ChoiceCardTitle>
    </span>
  )
}

/**
 * Which of this machine's addresses the port is published on: a tailnet's,
 * a VPN's, a local network's, each said by who it faces. Picking one is
 * reviewed before anything runs.
 */
function OneAddress({
  access,
  busy,
  onReview,
  onCancel,
}: {
  access: DbAccess
  busy: boolean
  onReview: (address: string) => void
  onCancel?: () => void
}) {
  const current = access.exposure === "private" ? access.address : undefined
  const [picked, setPicked] = useState(current ?? access.addresses[0]?.address)
  const chosen = access.addresses.find((one) => one.address === picked)
  const heading = useId()
  return (
    <div
      role="group"
      aria-labelledby={heading}
      className="animate-rise space-y-3 border-t border-hairline pt-5"
      data-slot="reach-address"
    >
      <h4 id={heading} className="text-sm font-medium">
        The address it is published on
      </h4>
      <ChoiceGrid columns={2} role="group" aria-label="This machine's addresses">
        {access.addresses.map((one) => (
          <AddressCard
            key={one.address}
            address={one}
            selected={one.address === picked}
            current={one.address === current}
            disabled={busy}
            onClick={() => setPicked(one.address)}
          />
        ))}
      </ChoiceGrid>
      {chosen?.network === "uplink" && (
        <FormNote tone="warning">
          A cloud provider often maps a public address onto this one, which would make it as
          reachable as Anywhere. Pick a tailnet or a VPN address if one is listed.
        </FormNote>
      )}
      {(chosen?.network === "tailnet" || chosen?.network === "vpn") && (
        <FormNote>
          Docker publishes only on an address the machine holds: if{" "}
          {chosen.interface ?? "the tunnel"} is down when the container starts, it does not start
          until it is restarted.
        </FormNote>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          onClick={() => picked && onReview(picked)}
          disabled={!picked || picked === current || busy}
          pending={busy}
        >
          Publish on {picked ?? "this address"}
        </Button>
        {onCancel && (
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            Cancel
          </Button>
        )}
      </div>
    </div>
  )
}

function AddressCard({
  address,
  selected,
  current,
  disabled,
  onClick,
}: {
  address: DbHostAddress
  selected: boolean
  current: boolean
  disabled: boolean
  onClick: () => void
}) {
  const words = addressWords(address)
  return (
    <ChoiceCard selected={selected} disabled={disabled} onClick={onClick}>
      <span className="flex min-w-0 items-center gap-2">
        <ProductLogo
          id={address.network === "tailnet" ? "tailscale" : undefined}
          fallback={Router}
          size="sm"
        />
        <ChoiceCardTitle>
          <span className="font-mono">{address.address}</span>
        </ChoiceCardTitle>
      </span>
      <ChoiceCardHint>
        {words.label}
        {address.interface ? ` · ${address.interface}` : ""}
        {current ? " · published here now" : ""}. {words.hint}
      </ChoiceCardHint>
    </ChoiceCard>
  )
}

/**
 * The list the gateway keeps the port to, one row per address or network,
 * drawn the way the dashboard's own allowlist is: where those addresses are,
 * the tailnet as Tailscale and the rest as what they are.
 *
 * It is a draft until it is reviewed, so a list can be built up and taken
 * apart without the gateway being loaded at every step. The address this
 * request came from is offered by name, since it is the one most often meant
 * and the one most often mistyped.
 */
function AllowedAddresses({
  access,
  busy,
  onReview,
  onCancel,
}: {
  access: DbAccess
  busy: boolean
  onReview: (allow: string[]) => void
  onCancel?: () => void
}) {
  const [draft, setDraft] = useState(access.allow)
  const [adding, setAdding] = useState("")
  const candidate = adding.trim()
  const problem = sourceProblem(candidate, draft)
  const client = access.client
  const offerClient =
    client && !draft.includes(client) && !sourceProblem(client, draft) && client !== candidate
  const changed = !sameList(draft, access.allow)
  const heading = useId()
  const port = access.port

  const add = (entry: string) => {
    if (!entry || sourceProblem(entry, draft)) return
    setDraft([...draft, entry])
    setAdding("")
  }

  return (
    <div
      role="group"
      aria-labelledby={heading}
      className="animate-rise space-y-3 border-t border-hairline pt-5"
      data-slot="reach-allowlist"
    >
      <h4 id={heading} className="text-sm font-medium">
        The addresses that may reach it
      </h4>
      {draft.length > 0 ? (
        <RowList aria-label="Allowed addresses">
          {draft.map((entry) => {
            const kind = cidrKind(entry)
            return (
              <Row
                key={entry}
                className="px-0 py-2"
                leading={<ProductLogo id={kind.product} fallback={kind.glyph} size="sm" />}
                title={<CidrText cidr={entry} />}
                subtitle={entry === client ? `${kind.label} · the address you are on` : kind.label}
                trailing={
                  <IconAction
                    label={`Remove ${entry}`}
                    disabled={busy}
                    onClick={() => setDraft(draft.filter((other) => other !== entry))}
                  >
                    <Cross />
                  </IconAction>
                }
              />
            )
          })}
        </RowList>
      ) : (
        <FormNote>
          {`No address is on the list yet. Add the ones that connect to ${port ? `port ${port}` : "it"}: an office, an application server, your own.`}
        </FormNote>
      )}
      <form
        className="min-w-0"
        onSubmit={(event) => {
          event.preventDefault()
          add(candidate)
        }}
      >
        <Field label="Add an address or a network" htmlFor="reach-allow-add">
          <div className="flex min-w-0 gap-2">
            <Input
              id="reach-allow-add"
              value={adding}
              placeholder="203.0.113.7 or 198.51.100.0/24"
              autoFocus={access.exposure !== "allowlist"}
              aria-invalid={Boolean(problem) || undefined}
              aria-describedby={problem ? "reach-allow-problem" : undefined}
              onChange={(event) => setAdding(event.target.value)}
              className="min-w-0 font-mono text-body"
            />
            <Button
              type="submit"
              variant="outline"
              disabled={!candidate || Boolean(problem) || busy}
            >
              Add
            </Button>
          </div>
        </Field>
        {problem && (
          <FormNote id="reach-allow-problem" tone="danger" className="pt-1.5">
            {problem}
          </FormNote>
        )}
      </form>
      {offerClient && (
        <FormNote>
          You are connected from <span className="font-mono text-foreground">{client}</span>.{" "}
          <button
            type="button"
            className="rounded-sm font-medium text-foreground underline focus-ring"
            onClick={() => add(client)}
            disabled={busy}
          >
            Add it
          </button>
        </FormNote>
      )}
      {access.gateway.alsoAdmitted.length > 0 && (
        <FormNote>
          Also let in, whatever the list says: {joinWords(access.gateway.alsoAdmitted)} — the
          networks this dashboard is reached from, which can change the database through it anyway.
        </FormNote>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          onClick={() => onReview(draft)}
          disabled={draft.length === 0 || !changed || busy}
          pending={busy}
        >
          {access.exposure === "allowlist" ? "Save the list" : "Allow only these addresses"}
        </Button>
        {access.exposure === "allowlist" && changed && (
          <Button variant="ghost" onClick={() => setDraft(access.allow)} disabled={busy}>
            Discard
          </Button>
        )}
        {onCancel && (
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            Cancel
          </Button>
        )}
      </div>
    </div>
  )
}

/**
 * Who can reach the server's port, drawn as the way in: this machine's own
 * programs and the outside on the left, the container that publishes the
 * port in the middle — Docker's whale, its name, the binding — and the server
 * itself, as its product, at the end.
 *
 * The outside's line is the answer to the page's question. Dashed, with the
 * globe a ring not yet there, while the port is on loopback: nothing outside
 * gets in. Amber while it is published to anywhere, because it works and
 * should not be relied on (§3's amber, as a project's Runtime draws a port
 * that bypasses the proxy). Solid and plain for one network or a list, which
 * is the arrangement the page recommends — and a list draws everyone else
 * too, dashed, refused by the gateway, so the picture says both halves. A
 * server on another machine is one line from here to it, since where it
 * listens is that machine's to say.
 */
function ReachPicture({
  access,
  exposure,
  changing,
}: {
  access?: DbAccess
  exposure: string
  changing: boolean
}) {
  const { conn, engine, summary, status } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const localMark = useRef<HTMLDivElement>(null)
  const outsideMark = useRef<HTMLDivElement>(null)
  const refusedMark = useRef<HTMLDivElement>(null)
  const boxMark = useRef<HTMLDivElement>(null)
  const serverMark = useRef<HTMLDivElement>(null)
  const remote = exposure === "remote"
  const listed = exposure === "allowlist"
  const one = exposure === "private"
  const anywhere = exposure === "public"
  const open = anywhere || listed || one
  const running = status.state === "running"
  const port = access?.port ?? (Number(conn.port) || undefined)
  const boxed = Boolean(access?.container) && !remote
  const bind =
    anywhere || listed ? "0.0.0.0" : one ? (access?.address ?? "one address") : "127.0.0.1"
  const firewall = access?.firewall
  const wall = !firewall
    ? undefined
    : !firewall.active
      ? "no firewall in the way"
      : `${firewall.backend ?? "firewall"} ${firewall.open ? "lets it in" : "keeps it out"}`
  const network = access?.addresses.find((a) => a.address === access.address)
  const allow = access?.allow ?? []

  const local = (
    <WireNode
      key="local"
      nodeRef={localMark}
      align="end"
      mark={<WireHost size="md" />}
      eyebrow="This server"
      title="Programs and linked deployments"
      hint={remote ? "the dashboard dials it" : "over loopback"}
    />
  )
  const outside = remote ? undefined : listed ? (
    <WireNode
      key="outside"
      nodeRef={outsideMark}
      align="end"
      mark={
        <WireMark size="md" className="animate-rise">
          <Shield />
        </WireMark>
      }
      eyebrow="Allowed addresses"
      title={
        <span className="font-mono text-xs wrap-anywhere">
          {allow.slice(0, 2).join(", ")}
          {allow.length > 2 ? ` +${allow.length - 2}` : ""}
        </span>
      }
      hint={`${allow.length} on the list`}
    />
  ) : one ? (
    <WireNode
      key="outside"
      nodeRef={outsideMark}
      align="end"
      mark={
        network?.network === "tailnet" ? (
          <WireMark size="md" tone="logo" className="animate-rise">
            <ProductGlyph id="tailscale" />
          </WireMark>
        ) : (
          <WireMark size="md" className="animate-rise">
            <Router />
          </WireMark>
        )
      }
      eyebrow={network ? addressWords(network).label : "One network"}
      title={<span className="font-mono text-xs wrap-anywhere">{access?.address}</span>}
      hint={network?.interface ? `on ${network.interface}` : "one address of this machine"}
    />
  ) : (
    <WireNode
      key="outside"
      nodeRef={outsideMark}
      align="end"
      mark={
        anywhere ? (
          <WireMark size="md" tone="warning" className="animate-rise">
            <Globe />
          </WireMark>
        ) : (
          <WirePlaceholder size="md" className="animate-rise">
            <Globe />
          </WirePlaceholder>
        )
      }
      eyebrow="Anywhere"
      title={
        anywhere ? (
          <span className="font-mono text-xs">
            {access?.publicAddresses.length ? access.publicAddresses.join(", ") : "every interface"}
          </span>
        ) : (
          "Other machines"
        )
      }
      hint={
        <span className={cn(anywhere && "text-warning")}>
          {anywhere ? (wall ?? "reachable") : "cannot reach it"}
        </span>
      }
    />
  )
  const refused = listed && (
    <WireNode
      key="refused"
      nodeRef={refusedMark}
      align="end"
      mark={
        <WirePlaceholder size="md" className="animate-rise">
          <Globe />
        </WirePlaceholder>
      }
      eyebrow="Everyone else"
      title="Other addresses"
      hint="refused by the gateway"
    />
  )
  const box = (
    <WireNode
      nodeRef={boxMark}
      align="center"
      mark={
        <WireMark size="md" tone="logo">
          <ProductGlyph id="docker" />
        </WireMark>
      }
      eyebrow="Container"
      title={<span className="font-mono text-xs wrap-anywhere">{access?.container}</span>}
      hint={
        <span className="font-mono">
          {bind}:{port ?? "?"}
        </span>
      }
    />
  )
  const server = (
    <WireNode
      nodeRef={serverMark}
      align="start"
      mark={
        <WireMark size="md" tone="logo">
          <EngineGlyph engine={engine} />
        </WireMark>
      }
      eyebrow={`${engine.label}${summary?.versionNumber ? ` ${summary.versionNumber}` : ""}`}
      title={
        <span className="font-mono text-xs wrap-anywhere">
          {remote ? `${conn.host}:${conn.port}` : `:${port ?? conn.port}`}
        </span>
      }
      hint={remote ? "on its own machine" : "listens"}
    />
  )
  const inner = boxed ? boxMark : serverMark
  const start = [local, outside, refused].filter((node): node is React.JSX.Element => Boolean(node))
  return (
    <SettingPicture
      label="Who can reach it"
      containerRef={container}
      className="py-4"
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={localMark}
            toRef={inner}
            still={!running || changing}
          />
          {!remote && (
            <AnimatedBeam
              key={exposure}
              containerRef={container}
              fromRef={outsideMark}
              toRef={inner}
              still
              dashed={!open}
              tone={anywhere ? "warning" : "default"}
            />
          )}
          {listed && (
            <AnimatedBeam
              containerRef={container}
              fromRef={refusedMark}
              toRef={inner}
              still
              dashed
            />
          )}
          {boxed && (
            <AnimatedBeam
              containerRef={container}
              fromRef={boxMark}
              toRef={serverMark}
              still={!running || changing}
              delay={0.6}
            />
          )}
        </>
      }
      start={start}
      middle={boxed ? box : undefined}
      end={server}
    />
  )
}

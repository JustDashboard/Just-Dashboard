"use client"

import {
  ArrowDown,
  ArrowUp,
  CheckCircle,
  Hash,
  Linked,
  LockClosed,
  NetworkDevice,
  Slash,
  Warning,
} from "@/components/icons"
import { membersKnown, ownerConsequence } from "@/lib/docker-networks"
import { calendarDate, rate } from "@/lib/format"
import type { Container, DockerNetwork, NetworkDetail } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { DimActions, IconAction } from "@/components/icon-action"
import { LogoGlyph } from "@/components/logo"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelHeader } from "@/components/panel"
import { ProductLogo, containerProduct } from "@/components/product-logo"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { TileTrend } from "@/components/metrics/sparkline"
import { LiveBytes } from "@/components/overview/readings"
import { WireMark } from "@/components/deploy/wire"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Cidr } from "@/components/network/address"
import { RX, TX } from "@/components/network/rate-pair"
import type { NetworkRate } from "@/components/docker/network-band"
import type { ContainerTraffic } from "@/components/docker/network-members"
import {
  bareAddress,
  hostCapacity,
  isDashboardOwn,
  memberNames,
  networkOwner,
  refusesAttach,
  type MemberReading,
} from "@/components/docker/networks"

/**
 * What a network is carrying and holding, as four lines under its picture: in
 * and out on its bridge with their last two minutes, how much of its subnet
 * its members take, and whether they can call each other by name — which on
 * the default bridge they cannot, the commonest reason "db" does not resolve.
 *
 * They were the sheet's four tiles: a figure over a caption, twice with a
 * meter. Members was one of them; it is the page's identity line now.
 */
export function NetworkReadings({
  network,
  members,
  bridge,
  loading,
}: {
  network?: DockerNetwork
  members: MemberReading[]
  /** The bridge's rate now and its last two minutes; undefined where it has none or none yet. */
  bridge?: NetworkRate
  loading: boolean
}) {
  const waiting = loading || !network
  const v4 = network?.subnets.find((s) => !s.includes(":"))
  const capacity = v4 ? hostCapacity(v4) : 0
  const addressed = members.filter((m) => m.ipv4).length
  const unbridged = network && !network.bridge
  const names = network ? naming(network) : undefined
  const device = network?.bridge
  return (
    // One column until the window is wide: beside the rail two columns leave
    // a sentence a hundred pixels, and it is a sentence that is being read.
    <ReadingLines className="min-[1700px]:grid-cols-2 xl:grid-cols-1">
      <ReadingLine
        mark={
          <WireMark size="md" hue={RX}>
            <ArrowDown aria-hidden />
          </WireMark>
        }
        name="Received"
        hint={
          waiting
            ? undefined
            : unbridged
              ? "no bridge on this server to count"
              : device && <span className="font-mono">{device}</span>
        }
        trend={
          bridge && (
            <TileTrend
              values={bridge.points.map((p) => p.rx)}
              color={RX}
              label="Received over the last two minutes"
            />
          )
        }
      >
        {bridge ? (
          <>
            <LiveBytes value={bridge.rx} suffix="/s" /> in
          </>
        ) : (
          <NoReading>{waiting || !unbridged ? "Reading its bridge…" : "Not counted"}</NoReading>
        )}
      </ReadingLine>
      <ReadingLine
        mark={
          <WireMark size="md" hue={TX}>
            <ArrowUp aria-hidden />
          </WireMark>
        }
        name="Sent"
        hint={
          waiting
            ? undefined
            : unbridged
              ? "host, none and macvlan carry their own"
              : "everything its members sent each other and the world"
        }
        trend={
          bridge && (
            <TileTrend
              values={bridge.points.map((p) => p.tx)}
              color={TX}
              label="Sent over the last two minutes"
            />
          )
        }
      >
        {bridge ? (
          <>
            <LiveBytes value={bridge.tx} suffix="/s" /> out
          </>
        ) : (
          <NoReading>{waiting || !unbridged ? "Reading its bridge…" : "Not counted"}</NoReading>
        )}
      </ReadingLine>
      <ReadingLine
        mark={
          <WireMark size="md" hue="var(--tag-pink)">
            <Hash aria-hidden />
          </WireMark>
        }
        name="Addresses"
        hint={
          waiting ? undefined : v4 ? (
            <>
              <Cidr cidr={v4} />
              {network.gateway && (
                <>
                  {" "}
                  · gateway <span className="font-mono">{network.gateway}</span>
                </>
              )}
            </>
          ) : network.driver === "host" ? (
            "its containers share the server's addresses"
          ) : (
            "Docker gave it none"
          )
        }
      >
        {waiting ? (
          <NoReading>Reading its subnet…</NoReading>
        ) : capacity > 0 ? (
          <span className="numeric">
            {addressed.toLocaleString()} of {capacity.toLocaleString()} taken
          </span>
        ) : (
          <NoReading>No IPv4 subnet</NoReading>
        )}
      </ReadingLine>
      <ReadingLine
        mark={
          names?.tone === "warning" ? (
            <WireMark size="md" tone="warning">
              <Warning aria-hidden />
            </WireMark>
          ) : names?.tone === "ok" ? (
            <WireMark size="md" tone="success">
              <CheckCircle aria-hidden />
            </WireMark>
          ) : (
            <WireMark size="md">
              <NetworkDevice aria-hidden className="text-muted-foreground" />
            </WireMark>
          )
        }
        name="Names"
        hint={names?.hint}
      >
        {names ? names.words : <NoReading>Reading who answers…</NoReading>}
      </ReadingLine>
    </ReadingLines>
  )
}

/** Whether a network's members can call each other by name, and why not where they cannot. */
function naming(network: Pick<DockerNetwork, "name" | "driver">): {
  tone: "ok" | "warning" | "none"
  words: string
  hint: string
} {
  if (network.driver === "host") {
    return {
      tone: "none",
      words: "The server's own names",
      hint: "a container here is the server, on its ports",
    }
  }
  if (network.driver === "null") {
    return { tone: "none", words: "Nothing to name", hint: "a container here has no interface" }
  }
  if (network.name === "bridge") {
    return {
      tone: "warning",
      words: "No names on the default bridge",
      hint: "reached only by address, which changes on a recreate",
    }
  }
  return {
    tone: "ok",
    words: "Members answer by name",
    hint: "its container name, its service and any alias",
  }
}

/**
 * The members, one line each: the container as the product it runs, the
 * address it is reached at and the other networks it joins this one to, the
 * names the others can use — a connection string on this network says
 * `db:5432`, not an address that changes when the container is recreated —
 * and its state with what it is moving. A row opens the container.
 *
 * Every row of a list is one height: two lines, and a third for the names
 * where the list is too narrow to give them a column. In the sheet a member
 * was one, two or three: "also on" was a third line under some, a second
 * address a second line under others.
 */
export function NetworkMembers({
  network,
  members,
  detailed,
  membersError,
  traffic,
  containers,
  canDetach,
  canAttach,
  onAttach,
  onDetach,
}: {
  network: DockerNetwork
  members: MemberReading[]
  /** Whether the inspect has landed: before it, the names are not known yet. */
  detailed: boolean
  /** The inspect could list who is attached but not read the containers themselves. */
  membersError?: string
  traffic: Map<string, ContainerTraffic>
  containers: Map<string, Container>
  canDetach: boolean
  canAttach: boolean
  onAttach: () => void
  onDetach: (member: MemberReading) => void
}) {
  const named = network.name !== "bridge" && network.driver !== "host" && network.driver !== "null"
  return (
    <section aria-label="Members" className="@container flex min-w-0 flex-col gap-4 pb-4">
      {!membersKnown(network) && (
        <Notice tone="warning" title="Who is on it could not be read">
          Docker listed the network but not the containers on it, so it is not shown as unused and
          cannot be removed until a refresh reads them
          {network.membersError ? `: ${network.membersError}` : "."}
        </Notice>
      )}
      {membersError && (
        <Notice tone="warning" title="The members' details could not all be read">
          Docker listed who is attached but not the containers themselves, so their states, stacks
          and other networks are unknown here: {membersError}
        </Notice>
      )}
      {network.name === "bridge" && (
        <Notice tone="warning" title="No names on the default bridge">
          Containers here reach each other only by address, which changes when one is recreated. Put
          containers that talk to each other on a network of their own.
        </Notice>
      )}
      {members.length === 0 ? (
        membersKnown(network) && (
          <EmptyState
            icon={NetworkDevice}
            title="Nothing is attached"
            description={
              network.subnets[0] ? (
                <>
                  It holds <Cidr cidr={network.subnets[0]} /> and carries nothing.
                </>
              ) : undefined
            }
            action={
              canAttach && (
                <Button size="sm" onClick={onAttach}>
                  <Linked className="size-3.5" />
                  Attach a container
                </Button>
              )
            }
          />
        )
      ) : (
        <ChoiceList aria-label="Containers on this network">
          {members.map((member) => (
            <MemberRow
              key={member.id}
              member={member}
              named={named}
              detailed={detailed}
              reading={traffic.get(member.id)}
              container={containers.get(member.id)}
              canDetach={canDetach}
              onDetach={onDetach}
            />
          ))}
        </ChoiceList>
      )}
    </section>
  )
}

function MemberRow({
  member,
  named,
  detailed,
  reading,
  container,
  canDetach,
  onDetach,
}: {
  member: MemberReading
  named: boolean
  detailed: boolean
  reading?: ContainerTraffic
  container?: Container
  canDetach: boolean
  onDetach: (member: MemberReading) => void
}) {
  const running = member.state === "running"
  const names = memberNames(member)
  const guard = guardedMember(member)
  const address = bareAddress(member.ipv4 ?? member.ipv6)
  const second = member.ipv4 && member.ipv6 ? bareAddress(member.ipv6) : undefined
  return (
    <ChoiceRow
      // A height, not a minimum: an address in the mono face sets a taller
      // line than a name does, and a member with one was a pixel or two taller.
      // Where the names have no column of their own they are the row's third
      // line, on every row of a network that has names.
      className={cn("justify-center @lg:h-[3.75rem]", named ? "h-[5.25rem]" : "h-[3.75rem]")}
      href={`/docker/containers/${encodeURIComponent(member.id)}`}
      verb={`Open ${member.name}`}
      leading={
        member.dashboard ? (
          <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background">
            <LogoGlyph small className="h-4 text-brand" />
          </span>
        ) : (
          <ProductLogo id={container ? containerProduct(container) : "docker"} size="sm" />
        )
      }
      title={
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate">{member.name}</span>
          {member.dashboard && <Tag className="shrink-0">This dashboard</Tag>}
          {member.ingress && <Tag className="shrink-0">Shared ingress</Tag>}
        </span>
      }
      description={
        <span
          className="flex min-w-0 items-center gap-1.5"
          title={[
            address,
            second,
            member.others.length > 0 && `also on ${member.others.join(", ")}`,
          ]
            .filter(Boolean)
            .join(" · ")}
        >
          <span className="shrink-0 font-mono text-foreground/80">
            {address ?? (running ? "no address" : "holds no address")}
          </span>
          {second && <span className="shrink-0 font-mono @max-lg:hidden">{second}</span>}
          {member.others.length > 0 && (
            <span className="min-w-0 truncate">
              · also on <span className="font-mono">{member.others.join(", ")}</span>
            </span>
          )}
        </span>
      }
      trailing={
        <>
          {named && (
            <span
              data-slot="member-names"
              aria-label={`Answers to ${names.join(", ")}`}
              title={names.join(", ")}
              className="flex w-36 min-w-0 items-center gap-1 overflow-hidden @max-lg:hidden"
            >
              {member.unread ? (
                <Tag title="Its own inspect failed, so what it answers to is unknown">
                  aliases unread
                </Tag>
              ) : !detailed ? (
                <Skeleton className="h-3 w-20" />
              ) : (
                <>
                  {names.slice(0, 2).map((name) => (
                    <Tag key={name} mono className="min-w-0 shrink">
                      <span className="truncate">{name}</span>
                    </Tag>
                  ))}
                  {names.length > 2 && (
                    <span className="numeric shrink-0 text-micro text-muted-foreground">
                      +{names.length - 2}
                    </span>
                  )}
                </>
              )}
            </span>
          )}
          <span className="flex w-[9.5rem] flex-col items-start @max-lg:w-auto">
            <Status state={member.state} label={member.state} className="font-normal" />
            <span className="numeric h-4 truncate font-mono text-micro @max-lg:hidden">
              {reading && running && (
                <>
                  <span style={{ color: RX }}>↓ {rate(reading.rx)}</span>{" "}
                  <span style={{ color: TX }}>↑ {rate(reading.tx)}</span>
                </>
              )}
            </span>
          </span>
        </>
      }
      actions={
        canDetach && (
          <DimActions>
            {guard ? (
              // Focusable and not `disabled`, so the reason is reachable by
              // keyboard and under the pointer.
              <IconAction
                label={guard}
                aria-disabled
                className="text-muted-foreground opacity-40"
                onClick={() => undefined}
              >
                <LockClosed />
              </IconAction>
            ) : (
              <IconAction label={`Detach ${member.name}`} onClick={() => onDetach(member)}>
                <Slash />
              </IconAction>
            )}
          </DimActions>
        )
      }
    >
      {named && (
        <p
          className="truncate pl-11 text-hint text-muted-foreground @lg:hidden"
          title={names.join(", ")}
        >
          {member.unread ? (
            "aliases unread"
          ) : !detailed ? (
            "\u00a0"
          ) : (
            <>
              answers to <span className="font-mono text-foreground/80">{names.join(", ")}</span>
            </>
          )}
        </p>
      )}
    </ChoiceRow>
  )
}

/** Why a member offers no detach control: its owner put it there. */
function guardedMember(member: MemberReading) {
  if (member.dashboard) return "Part of the dashboard itself — it is never detached here"
  if (member.ingress) {
    return "The shared public Caddy — deployment routes reach their containers through it"
  }
  return undefined
}

/** One label column for every section, so the values stand on one edge down the page. */
const LIST = "grid-cols-[9.5rem_minmax(0,1fr)] gap-y-2 pt-3 max-sm:grid-cols-[7.5rem_minmax(0,1fr)]"

/**
 * What Docker was told when the network was made, for the reader who came to
 * check one setting: where its addresses come from, what acts on it next,
 * whether it reaches the internet and takes new members, then its driver's
 * options and its labels as they are written.
 */
export function NetworkConfiguration({
  network,
  detail,
}: {
  network: DockerNetwork
  /** The inspect, which alone has the driver's options. */
  detail?: NetworkDetail
}) {
  const options = Object.entries(detail?.options ?? {})
  const labels = Object.entries(network.labels ?? {})
  const owner = networkOwner(network)
  const consequence = ownerConsequence(network.owner)
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-4">
      <Panel plain>
        <PanelHeader title="Addresses" />
        <DetailList className={LIST}>
          <Detail label="Subnets" className="text-body">
            {network.subnets.length > 0 ? (
              <span className="flex flex-col">
                {network.subnets.map((s) => (
                  <Cidr key={s} cidr={s} />
                ))}
              </span>
            ) : network.driver === "host" ? (
              "the server's own"
            ) : (
              "none"
            )}
          </Detail>
          <Detail label="Gateway" className="font-mono text-body">
            {network.gateway || "—"}
          </Detail>
          <Detail label="IPv6" className="text-body">
            {network.ipv6 ? "on" : "off"}
          </Detail>
        </DetailList>
      </Panel>

      <Panel plain>
        <PanelHeader title="How it behaves" />
        <DetailList className={LIST}>
          <Detail label="Made by" className="text-body leading-relaxed">
            {owner.label}
            {consequence && <span className="block text-muted-foreground">{consequence}</span>}
          </Detail>
          <Detail label="Driver" className="text-body">
            {network.driver === "null" ? "none" : network.driver}
            {network.bridge && (
              <span className="ml-2 font-mono text-muted-foreground">{network.bridge}</span>
            )}
          </Detail>
          <Detail label="Reaches the internet" className="text-body">
            {network.internal ? "no — its members reach only each other" : "yes"}
          </Detail>
          {/*
            `docker network connect` works on any local network, compose's
            included; Docker's attachable flag only lets standalone containers
            onto a swarm overlay, so it is said only for one.
          */}
          <Detail label="Accepts new members" className="text-body">
            {isDashboardOwn(network)
              ? "no — the dashboard's own"
              : refusesAttach(network)
                ? "swarm services only"
                : network.scope === "swarm"
                  ? "yes, standalone containers too"
                  : "yes, while running"}
          </Detail>
          <Detail label="Scope" className="text-body">
            {network.scope || "local"}
          </Detail>
          <Detail label="Created" className="text-body">
            {network.created ? calendarDate(network.created) : "—"}
          </Detail>
          <Detail label="ID" className="font-mono text-xs break-all">
            {network.id}
          </Detail>
        </DetailList>
      </Panel>

      {options.length > 0 && (
        <Panel plain>
          <PanelHeader title="Driver options" />
          <DetailList className={LIST}>
            {options.map(([key, value]) => (
              <Detail
                key={key}
                label={key.replace(/^com\.docker\.network\./, "")}
                className="font-mono text-xs break-all"
              >
                {value}
              </Detail>
            ))}
          </DetailList>
        </Panel>
      )}

      {labels.length > 0 && (
        <Panel plain>
          <PanelHeader title="Labels" />
          <ul className="space-y-1 pt-3 font-mono text-hint leading-relaxed text-muted-foreground">
            {labels.map(([key, value]) => (
              <li key={key} className="truncate" title={`${key}=${value}`}>
                {key}={value}
              </li>
            ))}
          </ul>
        </Panel>
      )}
    </div>
  )
}

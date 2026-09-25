"use client"

import { Connection, NetworkDevice, Router, Servers, type Icon } from "@/components/icons"
import { networkOf } from "@/lib/clients"
import { cn } from "@/lib/utils"
import type { NetInterface } from "@/lib/types"
import { NETWORK_GLYPH } from "@/components/client-mark"
import {
  ProductGlyph,
  ProductLogo,
  hasProductLogo,
  processProduct,
} from "@/components/product-logo"

/**
 * What the Security pages are about, drawn as the products they are (§14): a
 * fail2ban jail as the service it watches, a network device as what made it,
 * an address as the network it is on, a peer's processes as the programs
 * they are. Each names nothing it cannot read off the thing itself — a jail
 * called `sshd` keeps a glyph, because OpenSSH has no mark in the collection
 * and a guessed one would be the drawing lying about the row.
 */

/**
 * The words a jail's name is built from, and the service each says it
 * watches: fail2ban's stock jails are named after the daemon whose log they
 * read (`nginx-http-auth`, `apache-auth`, `traefik-auth`), and a jail somebody
 * wrote for a container is usually named after the container's product.
 */
const JAIL_WORDS: Record<string, string> = {
  nginx: "nginx-static",
  apache: "apache",
  caddy: "caddy",
  traefik: "traefik",
  vaultwarden: "vaultwarden",
  bitwarden: "vaultwarden",
  nextcloud: "nextcloud",
  gitea: "gitea",
  forgejo: "forgejo",
  gitlab: "gitlab",
  grafana: "grafana",
  jellyfin: "jellyfin",
  portainer: "portainer",
  mysql: "mysql",
  mysqld: "mysql",
  mariadb: "mariadb",
  postgres: "postgresql",
  postgresql: "postgresql",
  mongodb: "mongodb",
  redis: "redis",
  docker: "docker",
  wordpress: "php",
}

/** The service a jail watches, by its name; nothing for `sshd`, `recidive` and the rest. */
export function jailProduct(name: string): string | undefined {
  const id = name
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .map((word) => JAIL_WORDS[word])
    .find(Boolean)
  return hasProductLogo(id) ? id : undefined
}

/**
 * What made a network device, from its name: Tailscale's tunnel, Docker's
 * bridge and the virtual pair it gives every container, a Kubernetes CNI.
 * A physical port, a WireGuard tunnel and the loopback are no product.
 */
export function interfaceProduct(name: string): string | undefined {
  const device = name.toLowerCase()
  if (/^tailscale/.test(device)) return "tailscale"
  if (/^(docker|br-|veth)/.test(device)) return "docker"
  if (/^(cni|flannel|cali|kube)/.test(device)) return "kubernetes"
  return undefined
}

/** The glyph for a kind of device, on the same tile a product's mark takes. */
export const INTERFACE_GLYPH: Record<NetInterface["kind"], Icon> = {
  physical: NetworkDevice,
  tunnel: Connection,
  bridge: Router,
  virtual: Router,
  loopback: Servers,
}

/** A network device as what made it, else as its kind. */
export function InterfaceMark({ device }: { device: NetInterface }) {
  return (
    <ProductLogo
      id={interfaceProduct(device.name)}
      size="sm"
      fallback={INTERFACE_GLYPH[device.kind] ?? NetworkDevice}
    />
  )
}

/**
 * An address in a table, with the network it is on drawn before it: the
 * tailnet as Tailscale's mark, the rest as a glyph for the place. A column of
 * forty addresses is scanned for the one that is not on the tailnet before
 * any of them is read, which is the whole reason the Connections page exists.
 */
export function Address({ ip, className }: { ip: string; className?: string }) {
  // wtmp writes a hostname where the resolver gave it one, and a boot
  // record's "from" is the kernel version: neither is anywhere, so neither
  // takes a mark.
  if (!/^[0-9a-f.:]+$/i.test(ip) || !/[.:]/.test(ip)) {
    return <span className={cn("font-mono", className)}>{ip}</span>
  }
  const network = networkOf(ip)
  const Glyph = NETWORK_GLYPH[network.kind]
  return (
    <span
      className={cn("inline-flex min-w-0 items-center gap-1.5", className)}
      title={network.label}
    >
      {network.product ? (
        <ProductGlyph id={network.product} />
      ) : (
        <Glyph aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
      )}
      <span className="truncate font-mono">{ip}</span>
    </span>
  )
}

/**
 * The programs holding a peer's sockets, each drawn as the product it is
 * where it is one — caddy, nginx, dockerd, postgres — and as its name where
 * it is not.
 */
export function ProcessList({ names }: { names: string[] }) {
  if (names.length === 0) return <span className="text-muted-foreground">—</span>
  return (
    <span className="inline-flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
      {names.map((name) => {
        const product = processProduct(name)
        return (
          <span key={name} className="inline-flex items-center gap-1">
            {product && <ProductGlyph id={product} />}
            <span className="truncate">{name}</span>
          </span>
        )
      })}
    </span>
  )
}

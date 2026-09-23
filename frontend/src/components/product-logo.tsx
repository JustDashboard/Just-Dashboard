"use client"

import { useState } from "react"
import { Box, type Icon } from "@/components/icons"
import { cn } from "@/lib/utils"

/**
 * The file in `public/logos/` for each product a reader picks by name: every
 * reviewed blueprint, keyed by its id; the database engines, keyed the way
 * `/databases/provision/options` and a connection's `driver` name them; and
 * Docker and Compose themselves, for an image or a stack that is none of these.
 *
 * A template catalogue of sixty-two names in one grey face is a wall of words:
 * n8n, Grafana and Redis are recognised by their marks long before their names
 * are read, which is the argument §14 makes for a language's logo on a
 * repository row. The artwork is the product's own, in its own colours, so no
 * hue here is this product's to choose and none of it is a token.
 *
 * The files are bundled rather than fetched because the page's image policy
 * allows this origin only, and because the product runs on networks that
 * cannot reach a CDN (§8). Most come from homarr-labs/dashboard-icons
 * (Apache-2.0), picked in the variant that reads on a dark ground. A few do
 * not, because the collection draws them as a wordmark too small to read at
 * 24px or not at all: Jupyter is its Simple Icons mark (CC0), SQLite and SQL
 * Server are devicon's (MIT), and MySQL is devicon's dolphin lifted to L 0.72 —
 * its own #00618A is the navy §14 says disappears on this ground.
 * `public/logos/NOTICE` carries every source and licence.
 *
 * Aliases say which product a blueprint *is*: Mongo Express is MongoDB's own
 * admin, whoami is Traefik's.
 *
 * The dashboard's own pages draw the products it is made of and reached
 * through the same way: Caddy, Next.js and Go for the three services in its
 * stack, Tailscale and Let's Encrypt for its certificate, GitHub for the
 * repository it updates from.
 */
const LOGOS: Record<string, string> = {
  actual: "actual-budget.svg",
  adminer: "adminer.svg",
  audiobookshelf: "audiobookshelf.svg",
  beszel: "beszel.svg",
  caddy: "caddy.svg",
  clickhouse: "clickhouse.svg",
  "code-server": "code-server.webp",
  cyberchef: "cyberchef.svg",
  directus: "directus.svg",
  docker: "docker.svg",
  "docker-compose": "docker-compose.webp",
  docuseal: "docuseal.svg",
  dozzle: "dozzle.svg",
  drawio: "drawio.svg",
  filebrowser: "filebrowser.svg",
  freshrss: "freshrss.svg",
  github: "github.svg",
  gitea: "gitea.svg",
  go: "go.svg",
  gotify: "gotify.svg",
  grafana: "grafana.svg",
  healthchecks: "healthchecks.svg",
  homepage: "homepage.webp",
  influxdb: "influxdb.svg",
  "it-tools": "it-tools.svg",
  jellyfin: "jellyfin.svg",
  jupyter: "jupyter.svg",
  kavita: "kavita.svg",
  "lets-encrypt": "lets-encrypt.svg",
  linkding: "linkding.svg",
  mariadb: "mariadb.svg",
  meilisearch: "meilisearch.svg",
  memos: "memos.webp",
  metabase: "metabase.svg",
  "minecraft-bedrock": "minecraft.webp",
  "minecraft-java": "minecraft.webp",
  minio: "minio.svg",
  "mongo-express": "mongodb.svg",
  mongodb: "mongodb.svg",
  mysql: "mysql.svg",
  n8n: "n8n.svg",
  navidrome: "navidrome.svg",
  nextcloud: "nextcloud.svg",
  nextjs: "nextjs.svg",
  "nginx-static": "nginx.svg",
  nocodb: "nocodb.svg",
  ntfy: "ntfy.svg",
  ollama: "ollama.svg",
  "open-webui": "open-webui.svg",
  opengist: "opengist.svg",
  oracle: "oracle.svg",
  pgadmin: "pgadmin.svg",
  phpmyadmin: "phpmyadmin.svg",
  portainer: "portainer.svg",
  postgres: "postgresql.svg",
  postgresql: "postgresql.svg",
  prometheus: "prometheus.svg",
  qdrant: "qdrant.svg",
  rabbitmq: "rabbitmq.svg",
  redis: "redis.svg",
  searxng: "searxng.svg",
  seerr: "seerr.svg",
  shlink: "shlink.svg",
  sqlite: "sqlite.svg",
  sqlserver: "sqlserver.svg",
  "stirling-pdf": "stirling-pdf.svg",
  syncthing: "syncthing.svg",
  tailscale: "tailscale.svg",
  traefik: "traefik.svg",
  trilium: "trilium.svg",
  typesense: "typesense.svg",
  "uptime-kuma": "uptime-kuma.svg",
  valkey: "valkey.svg",
  vaultwarden: "vaultwarden.svg",
  wallabag: "wallabag.svg",
  whoami: "traefik.svg",
}

/** Image names that are not their product's own key. */
const IMAGE_ALIASES: Record<string, string> = {
  mongo: "mongodb",
  nginx: "nginx-static",
  "portainer-ce": "portainer",
  "actual-server": "actual",
  "mssql-server": "sqlserver",
  "clickhouse-server": "clickhouse",
}

/**
 * Which product an image reference is, from its last path segment:
 * `ghcr.io/owner/n8n:1.2` is n8n. Anything this cannot name is drawn as a
 * Docker image, which is at least true of every one of them.
 */
export function imageProduct(reference: string) {
  const name = (reference.split("@")[0].split("/").pop() ?? "").split(":")[0].toLowerCase()
  const id = IMAGE_ALIASES[name] ?? name
  return id in LOGOS ? id : "docker"
}

/**
 * The products a set of images is, most-named first and each once: a stack of
 * three Postgres replicas and an API is Postgres and Docker, not four marks.
 */
export function imageProducts(references: string[]) {
  const counts = new Map<string, number>()
  for (const reference of references) {
    const id = imageProduct(reference)
    counts.set(id, (counts.get(id) ?? 0) + 1)
  }
  // Docker's whale says "an image", which every one of them is: it is only
  // worth a place when nothing more specific is.
  const named = [...counts.keys()].filter((id) => id !== "docker")
  const ids = named.length > 0 ? named : [...counts.keys()]
  return ids.sort((a, b) => (counts.get(b) ?? 0) - (counts.get(a) ?? 0))
}

/**
 * A product's logo on a recessed tile, the size of the mark a deployment card
 * carries (`ProjectMark`), so a template and the project it becomes are drawn
 * the same way.
 *
 * A product with no file, or a file that fails to load, keeps the tile with a
 * plain glyph in it: a card whose title starts at a different place from its
 * neighbours' is the ragged grid this replaced.
 */
export function ProductLogo({
  id,
  size = "md",
  fallback: Fallback = Box,
  className,
}: {
  /** A blueprint id, an engine or driver key, or nothing for a thing with no product. */
  id?: string
  size?: "sm" | "md"
  /**
   * The glyph for a thing that is no product — a volume, a network — so it
   * still takes the tile and lines up with the rows that have a logo.
   */
  fallback?: Icon
  className?: string
}) {
  // Which file failed, rather than whether one did: the settings panel's mark
  // changes product under the same component, and the next one may load.
  const [failed, setFailed] = useState<string>()
  const file = id ? LOGOS[id] : undefined
  return (
    <span
      aria-hidden="true"
      className={cn(
        "flex shrink-0 items-center justify-center rounded-lg border border-hairline bg-background",
        size === "sm" ? "size-8" : "size-10",
        className,
      )}
    >
      {file && file !== failed ? (
        // Same-origin and already sized for the tile: a plain element, as
        // `ProjectMark`'s favicon is.
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={`/logos/${file}`}
          alt=""
          loading="lazy"
          className={cn("object-contain", size === "sm" ? "size-4.5" : "size-6")}
          onError={() => setFailed(file)}
        />
      ) : (
        <Fallback className="size-4 text-muted-foreground" />
      )}
    </span>
  )
}

/**
 * A product's logo bare, inside a line of text: the host a repository lives on,
 * the issuer beside a certificate's state. The tile is for a mark that stands
 * beside a card's words; in a sentence it is a box in the middle of a line, so
 * this is the artwork alone at the line's own height.
 */
export function ProductGlyph({ id, className }: { id: string; className?: string }) {
  const file = LOGOS[id]
  if (!file) return null
  return (
    // eslint-disable-next-line @next/next/no-img-element
    <img
      src={`/logos/${file}`}
      alt=""
      aria-hidden="true"
      className={cn("size-3.5 shrink-0 object-contain", className)}
    />
  )
}

/**
 * The products inside one thing — a stack's services — as tiles that overlap,
 * the way a group of avatars does: the first is whole and each after it tucks
 * under its neighbour, so three marks take the width of two. Past three it
 * says how many more rather than drawing a wall.
 */
export function ProductLogos({ ids, size = "sm" }: { ids: string[]; size?: "sm" | "md" }) {
  const shown = ids.slice(0, 3)
  const more = ids.length - shown.length
  return (
    <span aria-hidden="true" className="flex shrink-0 items-center">
      {shown.map((id, index) => (
        <ProductLogo
          key={id}
          id={id}
          size={size}
          className={cn(index > 0 && (size === "sm" ? "-ml-3" : "-ml-4"), "ring-2 ring-card")}
        />
      ))}
      {more > 0 && <span className="numeric ml-1 text-micro text-muted-foreground">+{more}</span>}
    </span>
  )
}

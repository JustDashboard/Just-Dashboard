"use client"

import { useState } from "react"
import { Box } from "@/components/icons"
import { cn } from "@/lib/utils"

/**
 * The file in `public/logos/` for each product a reader picks by name: every
 * reviewed blueprint, keyed by its id; the five engines the database quick
 * setup offers, keyed the way `/databases/provision/options` names them; and
 * Docker itself, for an image on this server that is none of these.
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
 * (Apache-2.0), picked in the variant that reads on a dark ground. Two do not,
 * because the collection draws them as a wordmark too small to read at 24px:
 * Jupyter is its Simple Icons mark (CC0), and MySQL is devicon's dolphin (MIT)
 * lifted to L 0.72 — its own #00618A is the navy §14 says disappears on this
 * ground. `public/logos/NOTICE` carries every source and licence.
 *
 * Aliases say which product a blueprint *is*: Mongo Express is MongoDB's own
 * admin, whoami is Traefik's.
 */
const LOGOS: Record<string, string> = {
  actual: "actual-budget.svg",
  adminer: "adminer.svg",
  audiobookshelf: "audiobookshelf.svg",
  beszel: "beszel.svg",
  caddy: "caddy.svg",
  "code-server": "code-server.webp",
  cyberchef: "cyberchef.svg",
  directus: "directus.svg",
  docker: "docker.svg",
  docuseal: "docuseal.svg",
  dozzle: "dozzle.svg",
  drawio: "drawio.svg",
  filebrowser: "filebrowser.svg",
  freshrss: "freshrss.svg",
  gitea: "gitea.svg",
  gotify: "gotify.svg",
  grafana: "grafana.svg",
  healthchecks: "healthchecks.svg",
  homepage: "homepage.webp",
  influxdb: "influxdb.svg",
  "it-tools": "it-tools.svg",
  jellyfin: "jellyfin.svg",
  jupyter: "jupyter.svg",
  kavita: "kavita.svg",
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
  "nginx-static": "nginx.svg",
  nocodb: "nocodb.svg",
  ntfy: "ntfy.svg",
  ollama: "ollama.svg",
  "open-webui": "open-webui.svg",
  opengist: "opengist.svg",
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
  "stirling-pdf": "stirling-pdf.svg",
  syncthing: "syncthing.svg",
  traefik: "traefik.svg",
  trilium: "trilium.svg",
  typesense: "typesense.svg",
  "uptime-kuma": "uptime-kuma.svg",
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
  className,
}: {
  /** A blueprint id, or a quick-setup engine key. */
  id: string
  size?: "sm" | "md"
  className?: string
}) {
  // Which file failed, rather than whether one did: the settings panel's mark
  // changes product under the same component, and the next one may load.
  const [failed, setFailed] = useState<string>()
  const file = LOGOS[id]
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
        <Box className="size-4 text-muted-foreground" />
      )}
    </span>
  )
}

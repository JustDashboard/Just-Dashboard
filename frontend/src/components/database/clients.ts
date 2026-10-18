import { wordsProduct } from "@/lib/clients"
import type { DbTopology } from "@/lib/types"

/**
 * What a database session is, as the product to draw it with, read from the
 * name the client gave the server — PostgreSQL's `application_name`, a Redis
 * `CLIENT SETNAME`, MongoDB's `appName` — and from what the dashboard already
 * knows uses this database.
 *
 * A deployment or a container the consumers route found is drawn as what it
 * is (`shop-api` as Node.js), because a client that names itself after its
 * deployment is that deployment. Past those the name is read for a driver or
 * a tool, the way a key's name is (`keyProduct`): the language's driver first,
 * since "PostgreSQL JDBC Driver" is a Java program that names its database,
 * then the tool. A name with none of these keeps the session's glyph: a mark
 * is never drawn from a guess.
 */

/** Drivers and the frameworks around them: the language a program is written in. */
const DRIVER_WORDS: Record<string, string> = {
  jdbc: "java",
  hikari: "java",
  hikaripool: "java",
  jedis: "java",
  lettuce: "java",
  npgsql: "dotnet",
  psycopg: "python",
  psycopg2: "python",
  psycopg3: "python",
  asyncpg: "python",
  sqlalchemy: "python",
  pymongo: "python",
  redispy: "python",
  celery: "python",
  django: "django",
  sidekiq: "ruby",
  activerecord: "rails",
  rails: "rails",
  pgx: "go",
  gorm: "go",
  goredis: "go",
  nodepostgres: "nodejs",
  ioredis: "nodejs",
  noderedis: "nodejs",
  bullmq: "nodejs",
  mongoose: "nodejs",
  prisma: "nodejs",
  typeorm: "nodejs",
  sequelize: "nodejs",
  knex: "nodejs",
  drizzle: "nodejs",
  node: "nodejs",
  nodejs: "nodejs",
  laravel: "laravel",
  doctrine: "php",
  php: "php",
  postgrex: "elixir",
  ecto: "elixir",
}

/** Tools and products that open a database by name. */
const TOOL_WORDS: Record<string, string> = {
  psql: "postgres",
  pgdump: "postgres",
  pgdumpall: "postgres",
  pgrestore: "postgres",
  pgbasebackup: "postgres",
  pgbench: "postgres",
  pgreceivewal: "postgres",
  postgres: "postgres",
  postgresql: "postgres",
  pgadmin: "pgadmin",
  pgadmin4: "pgadmin",
  mysql: "mysql",
  mysqldump: "mysql",
  mysqlsh: "mysql",
  mariadb: "mariadb",
  mariadbdump: "mariadb",
  mongosh: "mongodb",
  mongo: "mongodb",
  mongodb: "mongodb",
  mongodump: "mongodb",
  mongorestore: "mongodb",
  mongoexport: "mongodb",
  mongoimport: "mongodb",
  mongoexpress: "mongo-express",
  rediscli: "redis",
  redisinsight: "redis",
  redis: "redis",
  postgresexporter: "prometheus",
  mysqldexporter: "prometheus",
  redisexporter: "prometheus",
  mongodbexporter: "prometheus",
  prometheus: "prometheus",
  grafana: "grafana",
  metabase: "metabase",
  n8n: "n8n",
  directus: "directus",
  supabase: "supabase",
  posthog: "posthog",
  nocodb: "nocodb",
  adminer: "adminer",
  phpmyadmin: "phpmyadmin",
  jupyter: "jupyter",
  gitea: "gitea",
  nextcloud: "nextcloud",
}

const wordsOf = (name: string) =>
  name
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean)

/** The product a client's own name says it is, or nothing for a name that says none. */
export function clientProduct(name: string | undefined): string | undefined {
  if (!name) return undefined
  const words = wordsOf(name)
  return wordsProduct(words, DRIVER_WORDS) ?? wordsProduct(words, TOOL_WORDS)
}

/** One of the things the consumers route says uses this database, as a session names it. */
export type KnownClient = { name: string; product?: string; href?: string; kind: string }

/**
 * The deployments and containers the consumers route found for this
 * database, by name in lower case, for a session that names itself after
 * one. This server's own processes are not a name a session gives.
 */
export function knownClients(topology: DbTopology | undefined, id: number) {
  const known = new Map<string, KnownClient>()
  for (const node of topology?.nodes ?? []) {
    if (node.connId === id || node.kind === "host" || node.kind === "database") continue
    known.set(node.name.toLowerCase(), {
      name: node.name,
      product: node.product,
      href: node.href,
      kind: node.kind,
    })
  }
  return known
}

/** What a client is: the deployment it names, else what its name says, else nothing. */
export function clientOf(
  name: string | undefined,
  known: ReadonlyMap<string, KnownClient>,
): { product?: string; known?: KnownClient } {
  if (!name) return {}
  const match = known.get(name.toLowerCase())
  return { product: match?.product ?? clientProduct(name), known: match }
}

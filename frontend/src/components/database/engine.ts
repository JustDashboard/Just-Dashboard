import {
  Archive,
  ChartActivity,
  CodeBracket,
  FileText,
  GridSquare,
  Home,
  Key,
  Layout,
  ListOrdered,
  Logs,
  MagnifyingGlass,
  SettingsGear,
  Shield,
  Sparkles,
  Table,
  Terminal,
  Users,
  type Icon,
} from "@/components/icons"
import { hasProductLogo } from "@/components/product-logo"
import { DEFAULT_PORT } from "@/lib/db-dsn"
import type {
  DbCapabilities,
  DbCapabilityFlag,
  DbCapabilityName,
  DbConnection,
  DbDriver,
  DbDriverInfo,
} from "@/lib/types"

/**
 * Everything the frontend knows about a database engine, once.
 *
 * The section used to keep this in a dozen places — a label map on the fleet
 * card and another in the host dialog, a set of SQL-only paths in the layout,
 * the CLI and the environment variable in the connection-string panel, the
 * kind of store beside the engine picker — and they had already drifted: the
 * strip said "MySQL / MariaDB" and the card under it "MySQL". A page that
 * needs to know what an engine is called, what it calls its objects, which
 * pages it has or what it can do asks here, and no other file branches on a
 * driver's name.
 *
 * What an engine *can do* is the server's to say, and only the server's:
 * `GET /databases/drivers` carries a `capabilities` object per driver and per
 * flavour, and a connection's own summary carries it resolved. Nothing here
 * states a capability a second time. What this file holds is how an engine is
 * presented — its name and logo, its words, which page a flag opens, how a
 * program connects to it — and one short list per driver (`FLOOR`) of the
 * pages to draw in the moment before the catalogue has been read.
 *
 * A connection has a `driver`, which is how the dashboard talks to it, and a
 * `flavor`, which is what it is: MariaDB answers the `mysql` driver, Valkey
 * the `redis` one. The name, the logo and the command-line client follow the
 * flavour.
 */

/** How an engine keeps its data, which is what decides the pages it gets. */
export type EngineKind = "sql" | "document" | "keyvalue" | "cache" | "search"

/** The pages one database can have. `home` is the path with nothing after the id. */
export type SectionId =
  | "home"
  | "data"
  | "query"
  | "search"
  | "schema"
  | "diagram"
  | "generate"
  | "performance"
  | "advisor"
  | "logs"
  | "access"
  | "backups"
  | "settings"

export type SectionGroupId = "home" | "work" | "schema" | "insights" | "operate"

/** The part of the reader's place a section's address can hold. */
export type SelectionKey = "schema" | "table" | "db" | "collection" | "key"

/**
 * What goes in a section's query string: the selection (`schema`, `table`,
 * `db`, `collection`, `key`), a statement handed to Query once (`sql`), and
 * whatever else the section keeps there. `null`, `undefined` and `""` leave
 * the key out.
 */
export type SectionParams = Record<string, string | null | undefined>

/** The engine's own words, so a Redis page never says "table". */
export type EngineNouns = {
  /** What it holds: a table, a collection, a key. */
  object: string
  objects: string
  /** What its objects are grouped in: a schema, a database. */
  container: string
  containers: string
  /** One entry of an object: a row, a document, a key. */
  row: string
  rows: string
  /** What is run against it: a statement, a command, an operation. */
  statement: string
  statements: string
}

export type EngineSection = {
  id: SectionId
  /** In this engine's words: "Keys" on Redis, "Documents" on MongoDB. */
  title: string
  icon: Icon
  group: SectionGroupId
  /**
   * A working surface sized to the window — a grid, an editor, a canvas —
   * rather than a page that scrolls.
   */
  workbench: boolean
  /** The selection a link to this section carries over from where the reader is. */
  carries: readonly SelectionKey[]
}

/** The values a connect snippet is written from. */
export type ConnectParts = {
  /** The connection as a URL — the password masked until it is revealed. */
  url: string
  host: string
  port: string
  user: string
  database: string
}

/** One way a program connects: a client library's own few lines. */
export type ClientSnippet = {
  id: string
  /** The chip's word: the language, since that is what the reader picks by. */
  label: string
  code: (parts: ConnectParts) => string
}

export type Engine = {
  /** What it is: the flavour that answered, else the driver's own product. */
  id: string
  /** How the dashboard talks to it. Empty for an engine it can see and not open. */
  driver: DbDriver | ""
  label: string
  /**
   * The `ProductLogo` id, where this product's own artwork is bundled. One
   * with none is `undefined` and keeps the kind's glyph on the tile: a
   * Dragonfly server drawn with the Redis mark, or CockroachDB with the
   * PostgreSQL elephant, is a guessed logo with another product's name on it.
   */
  logo: string | undefined
  /**
   * The nearest of the five kinds the pages dispatch on. Print `kindWord`: an
   * engine the dashboard cannot open may be none of them (a graph, a broker).
   */
  kind: EngineKind
  kindWord: string
  /** The kind in a sentence: "a key–value store". */
  kindPhrase: string
  nouns: EngineNouns
  /** Empty for a file. */
  defaultPort: string
  dsnExample: string
  /** What the connection's `database` field names here: a file, an index, a service. */
  databaseField: string
  /** The command-line client: psql, mariadb, valkey-cli. */
  cli: string
  /** The variable an application reads its connection string from. */
  env: string
  /** The Monaco language its statements are written in. */
  editor: string
  capabilities: DbCapabilities
  /** The pages this engine has, in the rail's order. */
  sections: EngineSection[]
  /**
   * Whether the engine offers a feature (`roles`, `changeSets`, `streams`, …).
   * A list answers whether it holds anything, a word whether there is one.
   */
  can: (flag: DbCapabilityName) => boolean
  /** Whether the engine has a page. */
  has: (section: SectionId) => boolean
  section: (id: SectionId) => EngineSection | undefined
  /** Why a page this engine lacks is not there, for the state its address shows. */
  missing: (section: SectionId) => { thing: string; reason: string }
  /** The stored connection string as the URL every application expects. */
  url: (dsn: string) => string
  /** The line that opens a shell on it. */
  command: (parts: ConnectParts) => string
  clients: ClientSnippet[]
}

const KIND_WORD: Record<EngineKind, string> = {
  sql: "SQL",
  document: "documents",
  keyvalue: "key–value",
  cache: "cache",
  search: "search",
}

const KIND_PHRASE: Record<EngineKind, string> = {
  sql: "a SQL database",
  document: "a document database",
  keyvalue: "a key–value store",
  cache: "a cache",
  search: "a search engine",
}

/** The kind as the word a picker prints beside an engine's name. */
export function kindWord(kind: EngineKind): string {
  return KIND_WORD[kind]
}

/**
 * Every capability the server states, with nothing on: what an engine that
 * has not been heard from can do. Written out in full so that a flag the type
 * gains and this lacks, or the other way round, does not compile; the names
 * are held to what the server serves by the registry's tests.
 */
const NO_CAPABILITIES: DbCapabilities = {
  server: false,
  provision: false,
  inventoryConnect: false,
  hostAccount: false,
  fileBased: false,
  openByDefault: false,
  dump: false,
  sql: false,
  console: false,
  changeSets: false,
  keylessEdits: false,
  updateDefault: false,
  script: false,
  transactions: false,
  queryCancel: false,
  dollarQuoting: false,
  regexFilter: false,
  rowEstimate: false,
  cellRead: false,
  explainJSON: false,
  explainAnalyze: false,
  ddl: false,
  schemas: false,
  catalog: false,
  views: false,
  materializedViews: false,
  routines: false,
  triggers: false,
  sequences: false,
  enums: false,
  comments: false,
  indexes: false,
  extensions: false,
  stats: false,
  sessions: false,
  kill: false,
  cancel: false,
  locks: false,
  replication: false,
  tableStats: false,
  indexStats: false,
  maintenance: false,
  settings: false,
  settingsWrite: false,
  roles: false,
  privileges: false,
  statements: false,
  statementsReset: false,
  advisor: false,
  engineAdvisor: false,
  queryLog: false,
  clickhouseViews: false,
  sqliteFile: false,
  keys: false,
  keyTree: false,
  keyTypeFilter: false,
  keyMeta: false,
  valueDownload: false,
  keyEncoding: false,
  bulkKeys: false,
  streams: false,
  logicalDatabases: false,
  consoleClassify: false,
  serverInfo: false,
  commandStats: false,
  latency: false,
  queryLogReset: false,
  persistence: false,
  aofRewrite: false,
  memoryAnalysis: false,
  aclRules: false,
  pubsub: false,
  pubsubLive: false,
  monitor: false,
  documents: false,
  shellSyntax: false,
  collections: false,
  collectionOptions: false,
  aggregation: false,
  schemaAnalysis: false,
  indexUsage: false,
  indexHide: false,
  validation: false,
  profiler: false,
  orm: false,
  export: false,
  exportColumns: false,
  exportQuery: false,
  import: false,
  importMapping: false,
  importUpsert: false,
  importReplace: false,
  importCreateTable: false,
  dumpSchemaOnly: false,
  dumpDataOnly: false,
  dumpTables: false,
  dumpCompression: false,
  dumpDatabases: false,
  dumpUpload: false,
  restoreNewDatabase: false,
  serverDatabaseCreate: false,
  serverDatabaseConnect: false,
  copy: false,
  copyStructureOnly: false,
  catalogGroups: [],
  ddlOperations: [],
  maintenanceActions: [],
  ormTargets: [],
  exportFormats: [],
  importFormats: [],
  rowIdentity: false,
  returnsChangedRow: false,
  readOnlyScope: false,
  importAtomic: false,
  json: false,
  hashFieldTtl: false,
  commandReference: false,
}

/**
 * The name of every capability the server states. An engine's resolved
 * capabilities always carry all of them.
 */
export const CAPABILITY_NAMES = Object.keys(NO_CAPABILITIES) as DbCapabilityName[]

/**
 * The pages to draw for a driver before the catalogue has been read, as the
 * flags that open a page or a control of the shell itself. It is used when
 * there is nothing from the server to go on — the rail drawn from an address
 * on arrival, a catalogue that failed — and never once the catalogue is in
 * hand: there the server's table is the whole answer. Every other capability
 * is off until then, so nothing is offered that the server has not vouched for.
 */
const FLOOR: Record<DbDriver, readonly DbCapabilityFlag[]> = {
  postgres: ["sql", "console", "server", "dump", "orm", "advisor", "roles"],
  mysql: ["sql", "console", "server", "dump", "orm", "advisor", "roles"],
  sqlite: ["sql", "console", "dump", "orm", "advisor"],
  sqlserver: ["sql", "console", "server", "dump", "orm", "advisor", "roles"],
  clickhouse: ["sql", "console", "server", "dump", "orm", "advisor", "roles"],
  oracle: ["sql", "console", "server", "dump", "orm", "advisor"],
  mongodb: ["console", "server", "dump", "roles"],
  redis: ["console", "server", "dump", "roles"],
}

function floorOf(driver: DbDriver): Partial<DbCapabilities> {
  return Object.fromEntries(FLOOR[driver].map((flag) => [flag, true]))
}

const TABLES: EngineNouns = {
  object: "table",
  objects: "tables",
  container: "schema",
  containers: "schemas",
  row: "row",
  rows: "rows",
  statement: "statement",
  statements: "statements",
}

/** The engines whose tables are grouped by database rather than by schema. */
const TABLES_BY_DATABASE: EngineNouns = {
  ...TABLES,
  container: "database",
  containers: "databases",
}

type Flavor = {
  label: string
  cli?: string
}

type DriverSpec = {
  label: string
  kind: EngineKind
  nouns: EngineNouns
  dsnExample: string
  databaseField: string
  cli: string
  env: string
  editor: string
  /** Why a page a capability denies is not there. */
  absent?: Partial<Record<SectionId, string>>
  url?: (dsn: string) => string
  command: (cli: string, parts: ConnectParts) => string
  clients: ClientSnippet[]
  /** Every product the driver talks to. Its own is listed under its own id. */
  flavors: Record<string, Flavor>
}

/**
 * A value as one word of a POSIX shell command. A string between double
 * quotes is still read by the shell — `$(…)` and backticks in a password
 * would run when the line is pasted — so anything past the plain characters
 * goes between single quotes, where nothing is.
 */
function shellWord(value: string): string {
  return /^[A-Za-z0-9_@%+=:,./-]+$/.test(value) ? value : `'${value.replace(/'/g, "'\\''")}'`
}

/**
 * A value as a string literal of the snippet's language. JSON's escapes are
 * read the same way by JavaScript, Python and Go, so a quote or a backslash
 * in a password cannot end the string early.
 */
function literal(value: string): string {
  return JSON.stringify(value)
}

const DRIVERS: Record<DbDriver, DriverSpec> = {
  postgres: {
    label: "PostgreSQL",
    kind: "sql",
    nouns: TABLES,
    dsnExample: "postgres://user:password@127.0.0.1:5432/dbname?sslmode=disable",
    databaseField: "Database",
    cli: "psql",
    env: "DATABASE_URL",
    editor: "pgsql",
    command: (cli, { url }) => `${cli} ${shellWord(url)}`,
    clients: [
      {
        id: "node",
        label: "Node.js",
        code: ({ url }) =>
          `import pg from "pg"\n\nconst client = new pg.Client({ connectionString: ${literal(url)} })\nawait client.connect()`,
      },
      {
        id: "python",
        label: "Python",
        code: ({ url }) => `import psycopg\n\nconn = psycopg.connect(${literal(url)})`,
      },
    ],
    flavors: {
      postgres: { label: "PostgreSQL" },
      timescaledb: { label: "TimescaleDB" },
      cockroachdb: { label: "CockroachDB" },
      yugabytedb: { label: "YugabyteDB" },
    },
  },
  mysql: {
    label: "MySQL",
    kind: "sql",
    nouns: TABLES_BY_DATABASE,
    dsnExample: "user:password@tcp(127.0.0.1:3306)/dbname",
    databaseField: "Database",
    cli: "mysql",
    env: "DATABASE_URL",
    editor: "mysql",
    // The Go driver's own `user:pass@tcp(host)/db` is not a URL; every
    // application expects one. The driver takes the name and the password as
    // typed, and a URL does not: an `@` or a `:` in either would move where
    // the address starts, so both are percent-encoded on the way.
    url: (dsn) => {
      const m = /^(.*?)(?::(.*))?@tcp\((.*)\)\/(.*)$/.exec(dsn)
      if (!m) return dsn
      const [, user, password, at, db] = m
      const secret = password ? `:${encodeURIComponent(password)}` : ""
      return `mysql://${encodeURIComponent(user)}${secret}@${at}/${db}`
    },
    // A shell that takes fields asks for the password itself, so none lands
    // in a shell history.
    command: (cli, { host, port, user, database }) =>
      `${cli} --host=${shellWord(host)} --port=${port} --user=${shellWord(user || "root")} --password ${database && shellWord(database)}`.trim(),
    clients: [
      {
        id: "node",
        label: "Node.js",
        code: ({ url }) =>
          `import mysql from "mysql2/promise"\n\nconst conn = await mysql.createConnection(${literal(url)})`,
      },
      {
        id: "python",
        label: "Python",
        code: ({ url }) =>
          `from sqlalchemy import create_engine\n\nengine = create_engine(${literal(url.replace(/^mysql:/, "mysql+pymysql:"))})`,
      },
    ],
    flavors: {
      mysql: { label: "MySQL" },
      mariadb: { label: "MariaDB", cli: "mariadb" },
      percona: { label: "Percona Server" },
      tidb: { label: "TiDB" },
    },
  },
  sqlite: {
    label: "SQLite",
    kind: "sql",
    nouns: TABLES_BY_DATABASE,
    dsnExample: "/var/lib/myapp/data.db",
    databaseField: "Database file",
    cli: "sqlite3",
    env: "DATABASE_URL",
    editor: "sql",
    absent: {
      access:
        "A SQLite database is a file. Whoever can read the file reads everything in it, so there are no roles to manage.",
    },
    command: (cli, { database }) => `${cli} ${shellWord(database)}`,
    clients: [
      {
        id: "node",
        label: "Node.js",
        code: ({ database }) =>
          `import Database from "better-sqlite3"\n\nconst db = new Database(${literal(database)})`,
      },
      {
        id: "python",
        label: "Python",
        code: ({ database }) => `import sqlite3\n\nconn = sqlite3.connect(${literal(database)})`,
      },
    ],
    flavors: { sqlite: { label: "SQLite" } },
  },
  sqlserver: {
    label: "SQL Server",
    kind: "sql",
    nouns: TABLES,
    dsnExample: "sqlserver://user:password@127.0.0.1:1433?database=dbname",
    databaseField: "Database",
    cli: "sqlcmd",
    env: "DATABASE_URL",
    editor: "sql",
    command: (cli, { host, port, user, database }) =>
      `${cli} -S ${shellWord(`${host},${port}`)} -U ${shellWord(user || "sa")}${database ? ` -d ${shellWord(database)}` : ""}`,
    clients: [
      {
        id: "go",
        label: "Go",
        code: ({ url }) =>
          `import (\n\t"database/sql"\n\n\t_ "github.com/microsoft/go-mssqldb"\n)\n\ndb, err := sql.Open("sqlserver", ${literal(url)})`,
      },
    ],
    flavors: {
      sqlserver: { label: "SQL Server" },
      "azure-sql-edge": { label: "Azure SQL Edge" },
    },
  },
  clickhouse: {
    label: "ClickHouse",
    kind: "sql",
    nouns: TABLES_BY_DATABASE,
    dsnExample: "clickhouse://user:password@127.0.0.1:9000/default",
    databaseField: "Database",
    cli: "clickhouse-client",
    env: "CLICKHOUSE_URL",
    editor: "sql",
    command: (cli, { host, port, user, database }) =>
      `${cli} --host ${shellWord(host)} --port ${port} --user ${shellWord(user || "default")} --database ${shellWord(database || "default")} --ask-password`,
    clients: [
      {
        id: "go",
        label: "Go",
        code: ({ url }) =>
          `import (\n\t"database/sql"\n\n\t_ "github.com/ClickHouse/clickhouse-go/v2"\n)\n\ndb, err := sql.Open("clickhouse", ${literal(url)})`,
      },
      {
        id: "python",
        label: "Python",
        code: ({ url }) =>
          `from clickhouse_driver import Client\n\nclient = Client.from_url(${literal(url)})`,
      },
    ],
    flavors: { clickhouse: { label: "ClickHouse" } },
  },
  oracle: {
    label: "Oracle",
    kind: "sql",
    nouns: TABLES,
    dsnExample: "oracle://user:password@127.0.0.1:1521/ORCLPDB1",
    databaseField: "Service name",
    cli: "sqlplus",
    env: "DATABASE_URL",
    editor: "sql",
    absent: { access: "This dashboard does not manage Oracle's roles." },
    command: (cli, { host, port, user, database }) =>
      `${cli} ${shellWord(`${user || "system"}@//${host}:${port}/${database}`)}`,
    clients: [
      {
        id: "go",
        label: "Go",
        code: ({ url }) =>
          `import (\n\t"database/sql"\n\n\t_ "github.com/sijms/go-ora/v2"\n)\n\ndb, err := sql.Open("oracle", ${literal(url)})`,
      },
    ],
    flavors: { oracle: { label: "Oracle" } },
  },
  mongodb: {
    label: "MongoDB",
    kind: "document",
    nouns: {
      object: "collection",
      objects: "collections",
      container: "database",
      containers: "databases",
      row: "document",
      rows: "documents",
      statement: "operation",
      statements: "operations",
    },
    dsnExample: "mongodb://user:password@127.0.0.1:27017/dbname",
    databaseField: "Database",
    cli: "mongosh",
    env: "MONGODB_URI",
    editor: "json",
    absent: { advisor: "The advisor's checks are written for SQL engines." },
    command: (cli, { url }) => `${cli} ${shellWord(url)}`,
    clients: [
      {
        id: "node",
        label: "Node.js",
        code: ({ url }) =>
          `import { MongoClient } from "mongodb"\n\nconst client = new MongoClient(${literal(url)})\nawait client.connect()`,
      },
      {
        id: "python",
        label: "Python",
        code: ({ url }) =>
          `from pymongo import MongoClient\n\nclient = MongoClient(${literal(url)})`,
      },
    ],
    flavors: {
      mongodb: { label: "MongoDB" },
      ferretdb: { label: "FerretDB" },
    },
  },
  redis: {
    label: "Redis",
    kind: "keyvalue",
    nouns: {
      object: "key",
      objects: "keys",
      container: "database",
      containers: "databases",
      row: "key",
      rows: "keys",
      statement: "command",
      statements: "commands",
    },
    dsnExample: "redis://:password@127.0.0.1:6379/0",
    databaseField: "Database index",
    cli: "redis-cli",
    env: "REDIS_URL",
    editor: "redis",
    absent: { advisor: "The advisor's checks are written for SQL engines." },
    command: (cli, { url }) => `${cli} -u ${shellWord(url)}`,
    clients: [
      {
        id: "node",
        label: "Node.js",
        code: ({ url }) =>
          `import { createClient } from "redis"\n\nconst client = createClient({ url: ${literal(url)} })\nawait client.connect()`,
      },
      {
        id: "python",
        label: "Python",
        code: ({ url }) => `import redis\n\nclient = redis.from_url(${literal(url)})`,
      },
    ],
    flavors: {
      redis: { label: "Redis" },
      valkey: { label: "Valkey", cli: "valkey-cli" },
      keydb: { label: "KeyDB", cli: "keydb-cli" },
      dragonfly: { label: "Dragonfly" },
    },
  },
}

/**
 * Engines the dashboard can find on the machine and has no driver for. They
 * are named and drawn as themselves in the inventory, and have no pages.
 */
const UNOPENED: Record<string, { label: string; kind: EngineKind; kindWord?: string }> = {
  memcached: { label: "Memcached", kind: "cache" },
  elasticsearch: { label: "Elasticsearch", kind: "search" },
  opensearch: { label: "OpenSearch", kind: "search" },
  meilisearch: { label: "Meilisearch", kind: "search" },
  typesense: { label: "Typesense", kind: "search" },
  qdrant: { label: "Qdrant", kind: "search", kindWord: "vectors" },
  etcd: { label: "etcd", kind: "keyvalue" },
  couchdb: { label: "CouchDB", kind: "document" },
  duckdb: { label: "DuckDB", kind: "sql" },
  cassandra: { label: "Cassandra", kind: "sql", kindWord: "wide-column" },
  scylladb: { label: "ScyllaDB", kind: "sql", kindWord: "wide-column" },
  influxdb: { label: "InfluxDB", kind: "sql", kindWord: "time series" },
  neo4j: { label: "Neo4j", kind: "document", kindWord: "graph" },
  rabbitmq: { label: "RabbitMQ", kind: "keyvalue", kindWord: "message broker" },
  nats: { label: "NATS", kind: "keyvalue", kindWord: "messaging" },
  kafka: { label: "Kafka", kind: "keyvalue", kindWord: "event log" },
}

/** Names the same engine goes by in an image tag or a provisioning option. */
const ALIASES: Record<string, string> = {
  postgresql: "postgres",
  pgvector: "postgres",
  postgis: "postgres",
  mongo: "mongodb",
  mssql: "sqlserver",
  scylla: "scylladb",
}

type SectionSpec = {
  id: SectionId
  group: SectionGroupId
  title: string
  icon: Icon
  /** The kinds of engine that have the page at all. */
  kinds: readonly EngineKind[]
  /** A capability the engine must also have. Absent: the kind alone decides. */
  needs?: DbCapabilityFlag
  workbench?: boolean
  /** What the page is, in the sentence that says an engine has none. */
  thing: string
}

const OPENED: readonly EngineKind[] = ["sql", "document", "keyvalue"]

/** Every page a database can have, in the rail's order. */
const SECTIONS: SectionSpec[] = [
  {
    id: "home",
    group: "home",
    title: "Home",
    icon: Home,
    kinds: ["sql", "document", "keyvalue", "cache", "search"],
    thing: "home",
  },
  {
    id: "data",
    group: "work",
    title: "Data",
    icon: GridSquare,
    kinds: OPENED,
    workbench: true,
    thing: "data",
  },
  {
    id: "query",
    group: "work",
    title: "Query",
    icon: CodeBracket,
    kinds: OPENED,
    workbench: true,
    thing: "console",
  },
  {
    id: "search",
    group: "work",
    title: "Search",
    icon: MagnifyingGlass,
    kinds: ["sql"],
    thing: "table search",
  },
  {
    id: "schema",
    group: "schema",
    title: "Schema",
    icon: Table,
    kinds: ["sql", "document"],
    workbench: true,
    thing: "schema",
  },
  {
    id: "diagram",
    group: "schema",
    title: "Diagram",
    icon: Layout,
    kinds: ["sql"],
    workbench: true,
    thing: "diagram",
  },
  {
    id: "generate",
    group: "schema",
    title: "Generate",
    icon: Sparkles,
    kinds: ["sql"],
    needs: "orm",
    thing: "code generation",
  },
  {
    id: "performance",
    group: "insights",
    title: "Performance",
    icon: ChartActivity,
    kinds: OPENED,
    thing: "performance readings",
  },
  {
    id: "advisor",
    group: "insights",
    title: "Advisor",
    icon: Shield,
    kinds: OPENED,
    needs: "advisor",
    thing: "advisor",
  },
  {
    id: "logs",
    group: "insights",
    title: "Logs",
    icon: Logs,
    kinds: OPENED,
    workbench: true,
    thing: "logs",
  },
  {
    id: "access",
    group: "operate",
    title: "Access",
    icon: Users,
    kinds: OPENED,
    needs: "roles",
    thing: "roles",
  },
  {
    id: "backups",
    group: "operate",
    title: "Backups",
    icon: Archive,
    kinds: OPENED,
    needs: "dump",
    thing: "dumps",
  },
  {
    id: "settings",
    group: "operate",
    title: "Settings",
    icon: SettingsGear,
    kinds: ["sql", "document", "keyvalue", "cache", "search"],
    thing: "settings",
  },
]

/** The rail's groups, in order. Home stands alone above them and has no label. */
export const SECTION_GROUPS: { id: SectionGroupId; label?: string }[] = [
  { id: "home" },
  { id: "work", label: "Work" },
  { id: "schema", label: "Schema" },
  { id: "insights", label: "Insights" },
  { id: "operate", label: "Operate" },
]

/** Where an engine has its own word for a page. */
const WORDS: Partial<
  Record<EngineKind, Partial<Record<SectionId, { title: string; icon: Icon }>>>
> = {
  keyvalue: {
    data: { title: "Keys", icon: Key },
    query: { title: "Console", icon: Terminal },
  },
  document: {
    data: { title: "Documents", icon: FileText },
    query: { title: "Aggregations", icon: ListOrdered },
  },
}

/**
 * What of the reader's place a link into each section keeps. A table chosen
 * in Data is the table Schema opens on; a key means nothing to another page.
 */
const CARRIES: Partial<Record<EngineKind, Partial<Record<SectionId, readonly SelectionKey[]>>>> = {
  sql: {
    data: ["schema", "table"],
    schema: ["schema", "table"],
    query: ["schema"],
    search: ["schema"],
    diagram: ["schema"],
    generate: ["schema"],
    performance: ["schema"],
    advisor: ["schema"],
  },
  document: {
    data: ["db", "collection"],
    query: ["db", "collection"],
    schema: ["db", "collection"],
    performance: ["db"],
  },
  keyvalue: {
    data: ["db"],
    query: ["db"],
    performance: ["db"],
  },
}

const NOTHING: readonly SelectionKey[] = []

/** A flag is on, a word is given, a list holds something. */
function allowed(value: boolean | string | readonly string[] | undefined): boolean {
  if (Array.isArray(value)) return value.length > 0
  return value === true || (typeof value === "string" && value !== "")
}

function sectionsOf(kind: EngineKind, capabilities: DbCapabilities): EngineSection[] {
  return SECTIONS.filter(
    (spec) => spec.kinds.includes(kind) && (!spec.needs || allowed(capabilities[spec.needs])),
  ).map((spec) => ({
    id: spec.id,
    group: spec.group,
    title: WORDS[kind]?.[spec.id]?.title ?? spec.title,
    icon: WORDS[kind]?.[spec.id]?.icon ?? spec.icon,
    workbench: Boolean(spec.workbench),
    carries: CARRIES[kind]?.[spec.id] ?? NOTHING,
  }))
}

function build(
  fields: Omit<Engine, "sections" | "can" | "has" | "section" | "missing">,
  absent: Partial<Record<SectionId, string>> = {},
): Engine {
  const sections = fields.driver ? sectionsOf(fields.kind, fields.capabilities) : []
  const byId = new Map(sections.map((section) => [section.id, section]))
  const data = byId.get("data")
  return {
    ...fields,
    sections,
    // An own key only: a name that came from outside (`constructor`) is not
    // a capability for being something every object has.
    can: (flag) => Object.hasOwn(fields.capabilities, flag) && allowed(fields.capabilities[flag]),
    has: (id) => byId.has(id),
    section: (id) => byId.get(id),
    missing: (id) => {
      const spec = SECTIONS.find((s) => s.id === id)
      const thing = spec?.thing ?? id
      if (absent[id]) return { thing, reason: absent[id] }
      // The kind has no such page: say what it is and where its data is.
      if (spec && !spec.kinds.includes(fields.kind)) {
        return {
          thing,
          reason: `${fields.label} is ${fields.kindPhrase}.${
            data ? ` Its ${fields.nouns.objects} are under ${data.title}.` : ""
          }`,
        }
      }
      return { thing, reason: `${fields.label} does not offer it.` }
    },
  }
}

/**
 * A table's own entry for a name that came from outside — a flavour the
 * server reported, a segment of an address. A plain index would also answer
 * for `constructor` and `toString`, with whatever every object inherits.
 */
function own<T>(table: Record<string, T>, key: string): T | undefined {
  return Object.hasOwn(table, key) ? table[key] : undefined
}

/** The engine for a driver, flavour or engine id, with what the server said about it. */
export function engineOf(id: string, drivers?: DbDriverInfo[], given?: DbCapabilities): Engine {
  const key = own(ALIASES, id.toLowerCase()) ?? id.toLowerCase()
  const driver = (Object.keys(DRIVERS) as DbDriver[]).find(
    (d) => d === key || Object.hasOwn(DRIVERS[d].flavors, key),
  )

  if (!driver) {
    const other = own(UNOPENED, key)
    const kind = other?.kind ?? "sql"
    return build({
      id: key,
      driver: "",
      label: other?.label ?? id,
      logo: hasProductLogo(key) ? key : undefined,
      kind,
      kindWord: other?.kindWord ?? (other ? KIND_WORD[kind] : "database"),
      kindPhrase: other ? KIND_PHRASE[kind] : "a database",
      nouns: TABLES,
      defaultPort: "",
      dsnExample: "",
      databaseField: "Database",
      cli: "",
      env: "DATABASE_URL",
      editor: "plaintext",
      capabilities: NO_CAPABILITIES,
      url: (dsn) => dsn,
      command: () => "",
      clients: [],
    })
  }

  const spec = DRIVERS[driver]
  const flavor = own(spec.flavors, key) ?? spec.flavors[driver]
  const flavorId = Object.hasOwn(spec.flavors, key) ? key : driver
  const info = drivers?.find((d) => d.id === driver)
  // The server's table is the answer: what the catalogue says of this
  // flavour, and over it what the connection's own summary resolved, which
  // is the same reading for the product that actually answered. The floor
  // stands in only where no catalogue has been read.
  const served = info?.flavors.find((f) => f.id === flavorId)?.capabilities ?? info?.capabilities
  const capabilities: DbCapabilities = {
    ...NO_CAPABILITIES,
    ...(served ?? floorOf(driver)),
    ...given,
  }
  const cli = flavor.cli ?? spec.cli
  return build(
    {
      id: flavorId,
      driver,
      // The catalogue's own label for a driver covers every product it talks
      // to ("MySQL / MariaDB"); a connection is one of them.
      label: info?.flavors.find((f) => f.id === flavorId)?.label ?? flavor.label,
      // The driver's logo is its own product's alone; see `Engine.logo`.
      logo: hasProductLogo(flavorId) ? flavorId : undefined,
      kind: spec.kind,
      kindWord: KIND_WORD[spec.kind],
      kindPhrase: KIND_PHRASE[spec.kind],
      nouns: spec.nouns,
      defaultPort: info?.defaultPort ? String(info.defaultPort) : DEFAULT_PORT[driver],
      dsnExample: info?.dsnExample || spec.dsnExample,
      databaseField: spec.databaseField,
      cli,
      env: spec.env,
      editor: spec.editor,
      capabilities,
      url: spec.url ?? ((dsn) => dsn),
      command: (parts) => spec.command(cli, parts),
      clients: spec.clients,
    },
    spec.absent,
  )
}

/**
 * The engine behind a connection: its flavour where the server has asked it
 * what it is, its driver's own product until then.
 */
export function engineFor(
  conn: Pick<DbConnection, "driver" | "flavor" | "capabilities">,
  drivers?: DbDriverInfo[],
): Engine {
  // A flavour that is not one of this driver's is not believed over the driver.
  const known = own<DriverSpec>(DRIVERS, conn.driver)?.flavors
  const flavor =
    conn.flavor && known && Object.hasOwn(known, conn.flavor) ? conn.flavor : conn.driver
  return engineOf(flavor, drivers, conn.capabilities)
}

/** The pages an engine has, in the rail's order. */
export function sectionsFor(engine: Engine): EngineSection[] {
  return engine.sections
}

export type EngineSectionGroup = { id: SectionGroupId; label?: string; sections: EngineSection[] }

/** Pages under the rail's groups, with the groups that hold none of them left out. */
export function groupSections(sections: EngineSection[]): EngineSectionGroup[] {
  return SECTION_GROUPS.map((group) => ({
    ...group,
    sections: sections.filter((section) => section.group === group.id),
  })).filter((group) => group.sections.length > 0)
}

/** An engine's pages under the rail's groups. */
export function sectionGroups(engine: Engine): EngineSectionGroup[] {
  return groupSections(engine.sections)
}

/** Every page id, for a reader that has only an address to go on. */
export const SECTION_IDS: readonly SectionId[] = SECTIONS.map((spec) => spec.id)

const PARAM_ORDER = ["schema", "table", "db", "collection", "key", "sql"]

/**
 * The address of one database's page. The selection is written first and in
 * one order, so the same place is always the same string.
 */
export function sectionHref(
  id: number | string,
  section: SectionId = "home",
  params?: SectionParams,
): string {
  const path = section === "home" ? `/databases/${id}` : `/databases/${id}/${section}`
  if (!params) return path
  const keys = Object.keys(params).sort((a, b) => {
    const [x, y] = [PARAM_ORDER.indexOf(a), PARAM_ORDER.indexOf(b)]
    return (x < 0 ? PARAM_ORDER.length : x) - (y < 0 ? PARAM_ORDER.length : y)
  })
  const query = new URLSearchParams()
  for (const key of keys) {
    const value = params[key]
    if (value) query.set(key, value)
  }
  const text = query.toString()
  return text ? `${path}?${text}` : path
}

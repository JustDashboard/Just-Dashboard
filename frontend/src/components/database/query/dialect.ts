/**
 * How each engine writes SQL, as data: what quotes a name, what opens a
 * comment, how a text is escaped, how a statement asks for its first rows.
 *
 * The editor reads statements the way the reader's engine does only as far as
 * it has to in order to find the statement under the cursor, complete a name
 * and lay a statement out. What a statement *is* — where it ends, what it
 * would do — is always the server's reading (`POST /classify`); nothing here
 * decides whether a statement runs.
 */
export type Dialect = {
  /** The pair that quotes a name with a space or a capital in it. */
  quote: readonly [string, string]
  /** Other pairs the engine also reads as a quoted name. */
  alsoQuotes: readonly (readonly [string, string])[]
  /** `"text"` is a text and not a name. */
  doubleQuotedText: boolean
  /** `$$ … $$` and `$tag$ … $tag$` bodies. */
  dollarQuoting: boolean
  /** `#` opens a comment to the end of the line. */
  hashComments: boolean
  /** A backslash escapes the next character inside a text. */
  backslashEscapes: boolean
  /** `E'…'` is a text in which a backslash escapes, whatever plain texts do. */
  escapeStrings: boolean
  /** Names are folded to this case unless quoted; `keep` = compared as written. */
  folds: "lower" | "upper" | "keep"
  /** A statement reading the first `n` rows of a relation. */
  firstRows: (relation: string, n: number) => string
}

const limit = (relation: string, n: number) => `SELECT *\nFROM ${relation}\nLIMIT ${n};`

const GENERIC: Dialect = {
  quote: ['"', '"'],
  alsoQuotes: [],
  doubleQuotedText: false,
  dollarQuoting: false,
  hashComments: false,
  backslashEscapes: false,
  escapeStrings: false,
  folds: "keep",
  firstRows: limit,
}

const DIALECTS: Record<string, Dialect> = {
  postgres: { ...GENERIC, dollarQuoting: true, escapeStrings: true, folds: "lower" },
  mysql: {
    ...GENERIC,
    quote: ["`", "`"],
    doubleQuotedText: true,
    hashComments: true,
    backslashEscapes: true,
  },
  sqlite: {
    ...GENERIC,
    alsoQuotes: [
      ["`", "`"],
      ["[", "]"],
    ],
  },
  sqlserver: {
    ...GENERIC,
    quote: ["[", "]"],
    alsoQuotes: [['"', '"']],
    firstRows: (relation, n) => `SELECT TOP ${n} *\nFROM ${relation};`,
  },
  clickhouse: {
    ...GENERIC,
    quote: ["`", "`"],
    alsoQuotes: [['"', '"']],
    backslashEscapes: true,
  },
  oracle: {
    ...GENERIC,
    folds: "upper",
    firstRows: (relation, n) => `SELECT *\nFROM ${relation}\nFETCH FIRST ${n} ROWS ONLY;`,
  },
}

/** The dialect of a driver; one nobody has described is read as plain SQL. */
export function dialectOf(driver: string): Dialect {
  return Object.hasOwn(DIALECTS, driver) ? DIALECTS[driver] : GENERIC
}

const PLAIN = /^[a-z_][a-z0-9_$]*$/
const PLAIN_UPPER = /^[A-Z_][A-Z0-9_$#]*$/
const PLAIN_ANY = /^[A-Za-z_][A-Za-z0-9_$]*$/

/**
 * A name as it has to be written to mean itself: bare where the engine reads
 * it back unchanged, quoted where it would fold the case, or where the name
 * holds a space or is a word of the language.
 */
export function quoteName(name: string, dialect: Dialect, reserved?: ReadonlySet<string>): string {
  const plain =
    dialect.folds === "lower"
      ? PLAIN.test(name)
      : dialect.folds === "upper"
        ? PLAIN_UPPER.test(name)
        : PLAIN_ANY.test(name)
  if (plain && !reserved?.has(name.toUpperCase())) return name
  const [open, close] = dialect.quote
  return `${open}${name.split(close).join(close + close)}${close}`
}

/** `schema.table`, each part quoted as it needs; the schema is left out when it is the connection's own. */
export function qualifiedName(
  schema: string,
  name: string,
  dialect: Dialect,
  defaultSchema?: string,
  reserved?: ReadonlySet<string>,
): string {
  const bare = quoteName(name, dialect, reserved)
  if (!schema || schema === defaultSchema) return bare
  return `${quoteName(schema, dialect, reserved)}.${bare}`
}

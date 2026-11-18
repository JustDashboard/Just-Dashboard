"use client"

import { createContext, useContext, type CSSProperties } from "react"
import {
  Archive,
  BookOpen,
  Box,
  Clock,
  CodeBracket,
  Cpu,
  Database,
  DesktopDevice,
  Download,
  FileText,
  FileZip,
  Globe,
  Home,
  Image,
  Key,
  LockClosed,
  Logs,
  Music,
  Servers,
  SettingsGear,
  Table,
  Terminal,
  TextFormat,
  Trash,
  Video,
  Wrench,
  type Icon,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"

/**
 * What a file *is*, drawn: a folder, or a sheet of paper with the format on it.
 *
 * The listing used to draw Material Design Icons' file family — a glyph per
 * category in one flat tone, the same silhouette for a folder at every size.
 * It told a config from a certificate, but a directory of forty files read as
 * forty stencils, and the one thing a reader recognises a format by — its own
 * logo — was nowhere. The drawing is now the one every desktop file manager
 * has trained the eye on:
 *
 * - **A folder is a folder**, two-toned with its tab behind it, in the colour
 *   it was labelled (blue until somebody says otherwise). A folder whose name
 *   says what it holds carries that pressed into its face: the product's own
 *   mark for `.git`, `.docker` or `node_modules` (Simple Icons' single-colour
 *   drawings, so it can be one step darker than the face), a glyph from the
 *   product's vocabulary for `.ssh`, `etc` or `logs`. Build output and
 *   installed dependencies are graphite unless relabelled: nothing in them is
 *   yours to edit, and the grey says "walk past this one" before the name is
 *   read.
 * - **A file is a page**, its corner folded, with the format drawn on it — the
 *   language's or the product's own logo in its own colours where it has one
 *   (§14: a product is drawn as itself), a glyph in the format's colour where
 *   it does not, the lines of a text page where it is only text — and a band
 *   along its foot in the format's colour, carrying the extension once the
 *   icon is large enough to set it.
 *
 * The band's colour is by format rather than by category. A category (code,
 * data, media) was one hue for twenty formats, which is a legend to learn;
 * a TypeScript file in TypeScript's blue and a Python file in Python's is a
 * legend the reader already has. The hues are the product's tokens —
 * `--language-*` for a language GitHub colours, `--tag-*` for the rest — at
 * one lightness, so no format outshouts another.
 *
 * A symlink keeps its target's drawing and gains the alias arrow in its
 * corner: what a link points at is the useful fact. A broken one's corner is
 * red.
 */

// ---- Folder labels ---------------------------------------------------------

export const FOLDER_COLOURS = [
  "blue",
  "teal",
  "green",
  "yellow",
  "orange",
  "red",
  "pink",
  "purple",
  "graphite",
] as const
export type FolderColour = (typeof FOLDER_COLOURS)[number]

export const FOLDER_COLOUR_NAMES: Record<FolderColour, string> = {
  blue: "Blue",
  teal: "Teal",
  green: "Green",
  yellow: "Yellow",
  orange: "Orange",
  red: "Red",
  pink: "Pink",
  purple: "Purple",
  graphite: "Graphite",
}

/**
 * The labels the operator gave folders on this server, by path, and the
 * default colour chosen for all of them.
 *
 * A context rather than a prop because a folder is drawn in seven places —
 * the rows, the tiles, the inspector, the strip, the sidebar, the finder, the
 * viewer — and a label that reached only the ones someone remembered to
 * thread it through is a folder that is red in the listing and blue beside it.
 */
const FolderColours = createContext<{
  colours: Record<string, string>
  defaultColour?: FolderColour
}>({
  colours: {},
})

export function FolderColourProvider({
  colours,
  defaultColour,
  children,
}: {
  colours: Record<string, string>
  defaultColour?: string
  children: React.ReactNode
}) {
  return (
    <FolderColours value={{ colours, defaultColour: validFolderColour(defaultColour) }}>
      {children}
    </FolderColours>
  )
}

export const validFolderColour = (value: string | undefined): FolderColour | undefined =>
  (FOLDER_COLOURS as readonly string[]).includes(value ?? "") ? (value as FolderColour) : undefined

/** An unlabelled folder uses the global choice, then its kind, then blue. */
export function defaultFolderColour(
  name: string,
  path?: string,
  globalColour?: FolderColour,
): FolderColour {
  return globalColour ?? folderKind(name, path).colour ?? "blue"
}

/** The colour a folder is drawn in, given the labels: its own, else its default. */
export function folderColourOf(
  labelled: Record<string, string>,
  path: string | undefined,
  name: string,
  globalColour?: FolderColour,
): FolderColour {
  const own = path ? labelled[path] : undefined
  return validFolderColour(own) ?? defaultFolderColour(name, path, globalColour)
}

export function useFolderColour(path: string | undefined, name: string): FolderColour {
  const { colours, defaultColour } = useContext(FolderColours)
  return folderColourOf(colours, path, name, defaultColour)
}

// ---- Kinds -----------------------------------------------------------------

export type FileKind = {
  /** What this is, in the words somebody would use out loud. */
  label: string
  /** The band's colour: the format's own. */
  tone: string
  /** The format's logo, under `public/logos/`, drawn on the page in its own colours. */
  logo?: string
  /** A drawing for a format with no logo, in the band's colour. */
  glyph?: Icon
  /** The band's word, where the extension is not it ("LOCK" on a lockfile). */
  tag?: string
}

type FolderKind = {
  label: string
  /** A product's single-colour mark, under `public/logos/mono/`. */
  mark?: string
  emblem?: Icon
  /** The colour it takes until labelled. */
  colour?: FolderColour
}

const TONE = {
  slate: "var(--tag-slate)",
  red: "var(--tag-red)",
  amber: "var(--tag-amber)",
  green: "var(--tag-green)",
  cyan: "var(--tag-cyan)",
  blue: "var(--tag-blue)",
  violet: "var(--tag-violet)",
  pink: "var(--tag-pink)",
}
const lang = (name: string) => `var(--language-${name})`

const kind = (label: string, tone: string, more: Omit<FileKind, "label" | "tone"> = {}) => ({
  label,
  tone,
  ...more,
})

const TYPESCRIPT = kind("TypeScript", lang("typescript"), { logo: "typescript.svg" })
const TSX = kind("TypeScript React", lang("typescript"), { logo: "react.svg" })
const JAVASCRIPT = kind("JavaScript", lang("javascript"), { logo: "javascript.svg" })
const JSX = kind("JavaScript React", lang("javascript"), { logo: "react.svg" })
const PYTHON = kind("Python", lang("python"), { logo: "python.svg" })
const GO = kind("Go", lang("go"), { logo: "go.svg" })
const RUST = kind("Rust", lang("rust"), { logo: "rust.svg" })
const JAVA = kind("Java", lang("java"), { logo: "java.svg" })
const KOTLIN = kind("Kotlin", lang("kotlin"), { logo: "kotlin.svg" })
const SWIFT = kind("Swift", lang("swift"), { logo: "swift.svg" })
const C = kind("C", lang("c"), { logo: "c.svg" })
const CPP = kind("C++", lang("cpp"), { logo: "cplusplus.svg" })
const CSHARP = kind("C#", lang("csharp"), { logo: "csharp.svg" })
const PHP = kind("PHP", lang("php"), { logo: "php.svg" })
const RUBY = kind("Ruby", lang("ruby"), { logo: "ruby.svg" })
const LUA = kind("Lua", lang("lua"), { logo: "lua.svg" })
const HASKELL = kind("Haskell", lang("haskell"), { logo: "haskell.svg" })
const R = kind("R", lang("r"), { logo: "r.svg" })
const DART = kind("Dart", TONE.cyan, { logo: "dart.svg" })
const ELIXIR = kind("Elixir", TONE.violet, { logo: "elixir.svg" })
const SCALA = kind("Scala", TONE.red, { logo: "scala.svg" })
const PERL = kind("Perl", TONE.blue, { logo: "perl.svg" })
const ZIG = kind("Zig", TONE.amber, { logo: "zig.svg" })
const VUE = kind("Vue component", TONE.green, { logo: "vuejs.svg" })
const SVELTE = kind("Svelte component", TONE.red, { logo: "svelte.svg" })
const HTML = kind("HTML", lang("html"), { logo: "html5.svg" })
const CSS = kind("Stylesheet", lang("css"), { logo: "css3.svg" })
const SASS = kind("Sass stylesheet", lang("scss"), { logo: "sass.svg" })
const MARKDOWN = kind("Markdown", lang("markdown"), { logo: "markdown.svg" })
const JSON_ = kind("JSON", TONE.amber, { logo: "json.svg" })
const YAML = kind("YAML", TONE.red, { logo: "yaml.svg" })
const TOML = kind("TOML", TONE.amber, { glyph: SettingsGear })
const XML = kind("XML", TONE.amber, { logo: "xml.svg" })
const GRAPHQL = kind("GraphQL", TONE.pink, { logo: "graphql.svg" })
const SHELL = kind("Shell script", TONE.green, { logo: "bash.svg" })
const POWERSHELL = kind("PowerShell script", TONE.blue, { logo: "powershell.svg" })
const BATCH = kind("Batch script", TONE.slate, { glyph: Terminal })
const SQL = kind("SQL", TONE.amber, { glyph: Database })
const SQLITE = kind("SQLite database", TONE.cyan, { logo: "sqlite.svg" })
const CSV = kind("Tabular data", TONE.green, { glyph: Table })
const SHEET = kind("Spreadsheet", TONE.green, { glyph: Table })
const PDF = kind("PDF", TONE.red, { glyph: FileText })
const WORD = kind("Word document", TONE.blue)
const SLIDES = kind("Presentation", TONE.amber)
const TEXT = kind("Text", TONE.slate)
const LOG = kind("Log", TONE.slate, { glyph: Logs })
const LATEX = kind("LaTeX", TONE.cyan, { logo: "latex.svg" })
const IMAGE = kind("Image", TONE.pink, { glyph: Image })
const VECTOR = kind("Vector image", TONE.amber, { glyph: Image })
const VIDEO = kind("Video", TONE.violet, { glyph: Video })
const AUDIO = kind("Audio", TONE.cyan, { glyph: Music })
const FONT = kind("Font", TONE.violet, { glyph: TextFormat })
const ARCHIVE = kind("Archive", TONE.amber, { glyph: FileZip })
const DEBIAN = kind("Debian package", TONE.red, { logo: "debian.svg" })
const PACKAGE = kind("Package", TONE.red, { glyph: Box })
const DISK = kind("Disk image", TONE.slate, { glyph: Servers })
const CONFIG = kind("Configuration", TONE.cyan, { glyph: SettingsGear })
const UNIT = kind("systemd unit", TONE.cyan, { glyph: SettingsGear })
const TERRAFORM = kind("Terraform", TONE.violet, { logo: "terraform.svg" })
const SECRET = kind("Key or certificate", TONE.green, { glyph: Key })
const BINARY = kind("Binary", TONE.slate, { glyph: Cpu })
const JUPYTER = kind("Jupyter notebook", TONE.amber, { logo: "jupyter.svg", tag: "NB" })
const PLAIN = kind("File", TONE.slate)

const BY_EXTENSION: Record<string, FileKind> = {
  ts: TYPESCRIPT,
  mts: TYPESCRIPT,
  cts: TYPESCRIPT,
  tsx: TSX,
  js: JAVASCRIPT,
  mjs: JAVASCRIPT,
  cjs: JAVASCRIPT,
  jsx: JSX,
  py: PYTHON,
  pyi: PYTHON,
  pyw: PYTHON,
  ipynb: JUPYTER,
  go: GO,
  rs: RUST,
  java: JAVA,
  class: JAVA,
  jar: kind("Java archive", lang("java"), { logo: "java.svg" }),
  kt: KOTLIN,
  kts: KOTLIN,
  swift: SWIFT,
  c: C,
  h: C,
  cpp: CPP,
  cc: CPP,
  cxx: CPP,
  hpp: CPP,
  hh: CPP,
  cs: CSHARP,
  php: PHP,
  rb: RUBY,
  rake: RUBY,
  gemspec: RUBY,
  lua: LUA,
  hs: HASKELL,
  r: R,
  dart: DART,
  ex: ELIXIR,
  exs: ELIXIR,
  scala: SCALA,
  sc: SCALA,
  pl: PERL,
  pm: PERL,
  zig: ZIG,
  vue: VUE,
  svelte: SVELTE,
  html: HTML,
  htm: HTML,
  css: CSS,
  less: CSS,
  scss: SASS,
  sass: SASS,
  md: MARKDOWN,
  mdx: MARKDOWN,
  markdown: MARKDOWN,
  json: JSON_,
  jsonc: JSON_,
  json5: JSON_,
  ndjson: JSON_,
  yaml: YAML,
  yml: YAML,
  toml: TOML,
  xml: XML,
  plist: XML,
  graphql: GRAPHQL,
  gql: GRAPHQL,
  sh: SHELL,
  bash: SHELL,
  zsh: SHELL,
  fish: SHELL,
  ps1: POWERSHELL,
  bat: BATCH,
  cmd: BATCH,
  sql: SQL,
  dump: SQL,
  db: SQLITE,
  sqlite: SQLITE,
  sqlite3: SQLITE,
  csv: CSV,
  tsv: CSV,
  xlsx: SHEET,
  xls: SHEET,
  ods: SHEET,
  pdf: PDF,
  doc: WORD,
  docx: WORD,
  odt: WORD,
  rtf: WORD,
  ppt: SLIDES,
  pptx: SLIDES,
  odp: SLIDES,
  key: SECRET,
  txt: TEXT,
  rst: TEXT,
  adoc: TEXT,
  log: LOG,
  tex: LATEX,
  png: IMAGE,
  jpg: IMAGE,
  jpeg: IMAGE,
  gif: IMAGE,
  webp: IMAGE,
  avif: IMAGE,
  bmp: IMAGE,
  ico: IMAGE,
  tif: IMAGE,
  tiff: IMAGE,
  heic: IMAGE,
  psd: IMAGE,
  svg: VECTOR,
  mp4: VIDEO,
  webm: VIDEO,
  mkv: VIDEO,
  mov: VIDEO,
  avi: VIDEO,
  ogv: VIDEO,
  mp3: AUDIO,
  wav: AUDIO,
  flac: AUDIO,
  ogg: AUDIO,
  m4a: AUDIO,
  aac: AUDIO,
  opus: AUDIO,
  woff: FONT,
  woff2: FONT,
  ttf: FONT,
  otf: FONT,
  eot: FONT,
  zip: ARCHIVE,
  tar: ARCHIVE,
  gz: ARCHIVE,
  tgz: ARCHIVE,
  bz2: ARCHIVE,
  xz: ARCHIVE,
  zst: ARCHIVE,
  "7z": ARCHIVE,
  rar: ARCHIVE,
  deb: DEBIAN,
  rpm: PACKAGE,
  apk: PACKAGE,
  whl: kind("Python wheel", lang("python"), { logo: "python.svg" }),
  iso: DISK,
  img: DISK,
  qcow2: DISK,
  conf: CONFIG,
  cfg: CONFIG,
  cnf: CONFIG,
  ini: CONFIG,
  env: kind("Environment file", TONE.amber, { glyph: Key }),
  properties: CONFIG,
  rules: CONFIG,
  list: CONFIG,
  service: UNIT,
  socket: UNIT,
  timer: UNIT,
  mount: UNIT,
  target: UNIT,
  path: UNIT,
  tf: TERRAFORM,
  tfvars: TERRAFORM,
  hcl: TERRAFORM,
  pem: SECRET,
  crt: SECRET,
  cer: SECRET,
  csr: SECRET,
  p12: SECRET,
  pfx: SECRET,
  pub: SECRET,
  gpg: SECRET,
  asc: SECRET,
  kdbx: SECRET,
  so: BINARY,
  o: BINARY,
  a: BINARY,
  dll: BINARY,
  exe: BINARY,
  bin: BINARY,
  dat: BINARY,
  pyc: BINARY,
  wasm: BINARY,
  swp: BINARY,
}

const LOCKFILE = (logo: string) => kind("Lockfile", TONE.slate, { logo, tag: "LOCK" })
const HISTORY = (logo: string) => kind("Shell history", TONE.slate, { logo, tag: "HIST" })

/** Files a server keeps whose whole name says more than any extension. */
const BY_NAME: Record<string, FileKind> = {
  dockerfile: kind("Container build", TONE.blue, { logo: "docker.svg", tag: "DOCK" }),
  containerfile: kind("Container build", TONE.blue, { logo: "docker.svg", tag: "DOCK" }),
  ".dockerignore": kind("Docker exclusions", TONE.slate, { logo: "docker.svg", tag: "IGN" }),
  "docker-compose.yml": kind("Compose file", TONE.blue, { logo: "docker-compose.webp" }),
  "docker-compose.yaml": kind("Compose file", TONE.blue, { logo: "docker-compose.webp" }),
  "compose.yml": kind("Compose file", TONE.blue, { logo: "docker-compose.webp" }),
  "compose.yaml": kind("Compose file", TONE.blue, { logo: "docker-compose.webp" }),
  makefile: kind("Makefile", TONE.amber, { glyph: Wrench, tag: "MAKE" }),
  gnumakefile: kind("Makefile", TONE.amber, { glyph: Wrench, tag: "MAKE" }),
  "cmakelists.txt": kind("CMake build", TONE.green, { logo: "cmake.svg" }),
  caddyfile: kind("Caddy configuration", TONE.green, { logo: "caddy.svg", tag: "CADDY" }),
  "nginx.conf": kind("nginx configuration", TONE.green, { logo: "nginx.svg" }),
  ".htaccess": kind("Apache configuration", TONE.red, { logo: "apache.svg", tag: "HTA" }),
  "httpd.conf": kind("Apache configuration", TONE.red, { logo: "apache.svg" }),
  procfile: kind("Procfile", TONE.violet, { glyph: Terminal, tag: "PROC" }),
  gemfile: kind("Gemfile", lang("ruby"), { logo: "ruby.svg", tag: "GEM" }),
  "gemfile.lock": LOCKFILE("ruby.svg"),
  "package.json": kind("npm manifest", TONE.red, { logo: "npm.svg" }),
  "package-lock.json": LOCKFILE("npm.svg"),
  ".npmrc": kind("npm configuration", TONE.red, { logo: "npm.svg", tag: "RC" }),
  "bun.lock": LOCKFILE("bun.svg"),
  "bun.lockb": LOCKFILE("bun.svg"),
  "bunfig.toml": kind("Bun configuration", TONE.amber, { logo: "bun.svg" }),
  "yarn.lock": LOCKFILE("yarn.svg"),
  "pnpm-lock.yaml": LOCKFILE("pnpm.svg"),
  "pnpm-workspace.yaml": kind("pnpm workspace", TONE.amber, { logo: "pnpm.svg" }),
  "deno.json": kind("Deno configuration", TONE.slate, { logo: "deno.svg" }),
  "deno.lock": LOCKFILE("deno.svg"),
  "tsconfig.json": kind("TypeScript configuration", lang("typescript"), { logo: "typescript.svg" }),
  "go.mod": kind("Go module", lang("go"), { logo: "go.svg", tag: "MOD" }),
  "go.sum": LOCKFILE("go.svg"),
  "go.work": kind("Go workspace", lang("go"), { logo: "go.svg", tag: "WORK" }),
  "cargo.toml": kind("Cargo manifest", lang("rust"), { logo: "rust.svg" }),
  "cargo.lock": LOCKFILE("rust.svg"),
  "requirements.txt": kind("Python requirements", lang("python"), { logo: "python.svg" }),
  "pyproject.toml": kind("Python project", lang("python"), { logo: "python.svg" }),
  pipfile: kind("Pipfile", lang("python"), { logo: "python.svg", tag: "PIP" }),
  "pipfile.lock": LOCKFILE("python.svg"),
  "poetry.lock": LOCKFILE("python.svg"),
  "uv.lock": LOCKFILE("python.svg"),
  "composer.json": kind("Composer manifest", lang("php"), { logo: "php.svg" }),
  "composer.lock": LOCKFILE("php.svg"),
  "next.config.js": kind("Next.js configuration", TONE.slate, { logo: "nextjs.svg" }),
  "next.config.mjs": kind("Next.js configuration", TONE.slate, { logo: "nextjs.svg" }),
  "next.config.ts": kind("Next.js configuration", TONE.slate, { logo: "nextjs.svg" }),
  "vite.config.js": kind("Vite configuration", TONE.violet, { logo: "vitejs.svg" }),
  "vite.config.ts": kind("Vite configuration", TONE.violet, { logo: "vitejs.svg" }),
  "tailwind.config.js": kind("Tailwind configuration", TONE.cyan, { logo: "tailwindcss.svg" }),
  "tailwind.config.ts": kind("Tailwind configuration", TONE.cyan, { logo: "tailwindcss.svg" }),
  "claude.md": kind("Claude instructions", TONE.amber, { logo: "claude.svg" }),
  ".gitignore": kind("git exclusions", TONE.slate, { logo: "git.svg", tag: "IGN" }),
  ".gitattributes": kind("git attributes", TONE.slate, { logo: "git.svg", tag: "ATTR" }),
  ".gitmodules": kind("git submodules", TONE.slate, { logo: "git.svg", tag: "MOD" }),
  ".gitconfig": kind("git configuration", TONE.slate, { logo: "git.svg", tag: "CONF" }),
  ".env": kind("Environment file", TONE.amber, { glyph: Key, tag: "ENV" }),
  ".bashrc": kind("Shell startup", TONE.green, { logo: "bash.svg", tag: "RC" }),
  ".bash_profile": kind("Shell startup", TONE.green, { logo: "bash.svg", tag: "RC" }),
  ".bash_logout": kind("Shell startup", TONE.green, { logo: "bash.svg", tag: "RC" }),
  ".profile": kind("Shell startup", TONE.green, { logo: "bash.svg", tag: "RC" }),
  ".zshrc": kind("Shell startup", TONE.green, { logo: "bash.svg", tag: "RC" }),
  ".bash_history": HISTORY("bash.svg"),
  ".zsh_history": HISTORY("bash.svg"),
  ".python_history": HISTORY("python.svg"),
  ".node_repl_history": HISTORY("nodejs.svg"),
  ".psql_history": HISTORY("postgresql.svg"),
  ".mysql_history": HISTORY("mysql.svg"),
  ".vimrc": kind("Vim configuration", TONE.green, { logo: "vim.svg", tag: "RC" }),
  ".viminfo": kind("Vim state", TONE.slate, { logo: "vim.svg", tag: "INFO" }),
  ".editorconfig": kind("Editor configuration", TONE.cyan, { glyph: SettingsGear, tag: "CONF" }),
  license: kind("Licence", TONE.slate, { glyph: BookOpen, tag: "LIC" }),
  "license.md": kind("Licence", TONE.slate, { glyph: BookOpen }),
  "license.txt": kind("Licence", TONE.slate, { glyph: BookOpen }),
  copying: kind("Licence", TONE.slate, { glyph: BookOpen, tag: "LIC" }),
  readme: kind("Readme", TONE.cyan, { glyph: BookOpen, tag: "READ" }),
  authorized_keys: kind("Authorised SSH keys", TONE.green, { glyph: Key, tag: "SSH" }),
  known_hosts: kind("Known SSH hosts", TONE.green, { glyph: Key, tag: "SSH" }),
  passwd: kind("Account database", TONE.red, { glyph: Key, tag: "PWD" }),
  shadow: kind("Password hashes", TONE.red, { glyph: LockClosed, tag: "PWD" }),
  sudoers: kind("sudo rules", TONE.red, { glyph: LockClosed, tag: "SUDO" }),
  fstab: kind("Filesystem table", TONE.cyan, { glyph: SettingsGear, tag: "CONF" }),
  hosts: kind("Host names", TONE.cyan, { glyph: SettingsGear, tag: "CONF" }),
  hostname: kind("Host name", TONE.cyan, { glyph: SettingsGear, tag: "CONF" }),
  crontab: kind("Cron table", TONE.cyan, { glyph: Clock, tag: "CRON" }),
}

/** SSH key pairs are named by the tool, not by an extension. */
const SSH_KEY = /^id_(rsa|dsa|ecdsa|ed25519)(_sk)?$/

/** Suffixes a saved copy takes, under which the real format still lives. */
const BACKUP_SUFFIXES = new Set(["bak", "old", "orig", "save", "disabled", "dpkg-old", "rpmsave"])

/**
 * The kind of a file's name.
 *
 * Order matters: the whole name first (a `docker-compose.yml` is not merely
 * YAML), then the extension, then the extension *under* a backup suffix — an
 * `nginx.conf.bak` is still a config file, and the pass that forgets this is
 * how a directory of saved configs turns into a wall of blank sheets.
 */
export function fileKind(name: string): FileKind & { ext?: string } {
  const lower = name.toLowerCase()
  // A name known whole still has its extension to set on the band: a
  // `package.json` is npm's, and it is still JSON.
  if (BY_NAME[lower]) return { ...BY_NAME[lower], ext: extensionOf(lower) }
  if (SSH_KEY.test(lower)) return kind("SSH private key", TONE.green, { glyph: Key, tag: "KEY" })
  if (lower.startsWith(".env.")) return BY_NAME[".env"]

  const parts = lower.split(".")
  if (parts.length > 1) {
    const ext = parts[parts.length - 1]
    if (BY_EXTENSION[ext]) return { ...BY_EXTENSION[ext], ext }
    if (parts.length > 2 && BACKUP_SUFFIXES.has(ext)) {
      const under = parts[parts.length - 2]
      if (BY_EXTENSION[under]) return { ...BY_EXTENSION[under], tag: "BAK" }
    }
    // `.tar.gz` and friends: the archive is the pair, not the last word.
    if (parts.length > 2 && parts[parts.length - 2] === "tar") return ARCHIVE
    // A dotfile's "extension" is its whole name, which is no format at all.
    if (parts[0] !== "" || parts.length > 2) return { ...PLAIN, ext }
  }
  return PLAIN
}

/** The last word after a dot, unless the dot only opens a dotfile's name. */
function extensionOf(lower: string): string | undefined {
  const dot = lower.lastIndexOf(".")
  return dot > 0 ? lower.slice(dot + 1) : undefined
}

const BUILD = (label: string): FolderKind => ({ label, colour: "graphite" })

/** Folders whose name says what they hold. */
const FOLDERS_BY_NAME: Record<string, FolderKind> = {
  ".git": { label: "git repository", mark: "git.svg" },
  ".github": { label: "GitHub configuration", mark: "github.svg" },
  ".docker": { label: "Docker configuration", mark: "docker.svg" },
  docker: { label: "Docker", mark: "docker.svg" },
  node_modules: { label: "Installed packages", mark: "nodejs.svg", colour: "graphite" },
  ".nvm": { label: "Node versions", mark: "nodejs.svg" },
  ".npm": { label: "npm cache", mark: "npm.svg" },
  ".npm-global": { label: "npm packages", mark: "npm.svg" },
  ".yarn": { label: "Yarn cache", mark: "yarn.svg" },
  ".pnpm-store": { label: "pnpm store", mark: "pnpm.svg" },
  ".bun": { label: "Bun", mark: "bun.svg" },
  ".deno": { label: "Deno", mark: "deno.svg" },
  go: { label: "Go workspace", mark: "go.svg" },
  ".cargo": { label: "Cargo", mark: "rust.svg" },
  ".rustup": { label: "Rust toolchains", mark: "rust.svg" },
  ".venv": { label: "Python environment", mark: "python.svg", colour: "graphite" },
  venv: { label: "Python environment", mark: "python.svg", colour: "graphite" },
  __pycache__: { label: "Python bytecode", mark: "python.svg", colour: "graphite" },
  ".pyenv": { label: "Python versions", mark: "python.svg" },
  ".gem": { label: "Ruby gems", mark: "ruby.svg" },
  ".m2": { label: "Maven repository", mark: "java.svg" },
  ".gradle": { label: "Gradle", mark: "gradle.svg" },
  ".kube": { label: "Kubernetes configuration", mark: "kubernetes.svg" },
  ".terraform": { label: "Terraform state", mark: "terraform.svg", colour: "graphite" },
  ".pm2": { label: "PM2", mark: "pm2.svg" },
  ".claude": { label: "Claude", mark: "claude.svg" },
  ".vscode": { label: "Editor settings", mark: "vscode.svg" },
  ".vscode-server": { label: "Editor server", mark: "vscode.svg" },
  ".vim": { label: "Vim", mark: "vim.svg" },
  nvim: { label: "Neovim", mark: "neovim.svg" },
  nginx: { label: "nginx", mark: "nginx.svg" },
  caddy: { label: "Caddy", mark: "caddy.svg" },
  letsencrypt: { label: "Certificates", mark: "lets-encrypt.svg" },
  postgresql: { label: "PostgreSQL", mark: "postgresql.svg" },
  postgres: { label: "PostgreSQL", mark: "postgresql.svg" },
  mysql: { label: "MySQL", mark: "mysql.svg" },
  redis: { label: "Redis", mark: "redis.svg" },
  grafana: { label: "Grafana", mark: "grafana.svg" },
  prometheus: { label: "Prometheus", mark: "prometheus.svg" },
  tailscale: { label: "Tailscale", mark: "tailscale.svg" },
  ".next": BUILD("Build output"),
  dist: BUILD("Build output"),
  build: BUILD("Build output"),
  out: BUILD("Build output"),
  target: BUILD("Build output"),
  vendor: BUILD("Vendored dependencies"),
  ".cache": { label: "Cache", emblem: Clock },
  cache: { label: "Cache", emblem: Clock },
  tmp: { label: "Temporary files", emblem: Clock },
  temp: { label: "Temporary files", emblem: Clock },
  ".config": { label: "Configuration", emblem: SettingsGear },
  config: { label: "Configuration", emblem: SettingsGear },
  etc: { label: "Configuration", emblem: SettingsGear },
  "conf.d": { label: "Configuration", emblem: SettingsGear },
  home: { label: "Home directories", emblem: Home },
  root: { label: "root's home", emblem: Home },
  ".ssh": { label: "SSH keys", emblem: Key },
  ssh: { label: "SSH configuration", emblem: Key },
  ssl: { label: "Certificates", emblem: Key },
  certs: { label: "Certificates", emblem: Key },
  ".gnupg": { label: "GPG keys", emblem: Key },
  bin: { label: "Programs", emblem: Terminal },
  sbin: { label: "System programs", emblem: Terminal },
  scripts: { label: "Scripts", emblem: Terminal },
  src: { label: "Source", emblem: CodeBracket },
  lib: { label: "Libraries", emblem: CodeBracket },
  www: { label: "Web root", emblem: Globe },
  html: { label: "Web root", emblem: Globe },
  public: { label: "Public assets", emblem: Globe },
  static: { label: "Static assets", emblem: Globe },
  "sites-available": { label: "Sites", emblem: Globe },
  "sites-enabled": { label: "Sites", emblem: Globe },
  log: { label: "Logs", emblem: Logs },
  logs: { label: "Logs", emblem: Logs },
  downloads: { label: "Downloads", emblem: Download },
  documents: { label: "Documents", emblem: FileText },
  docs: { label: "Documentation", emblem: FileText },
  pictures: { label: "Pictures", emblem: Image },
  images: { label: "Images", emblem: Image },
  photos: { label: "Photos", emblem: Image },
  music: { label: "Music", emblem: Music },
  videos: { label: "Videos", emblem: Video },
  desktop: { label: "Desktop", emblem: DesktopDevice },
  backup: { label: "Backups", emblem: Archive },
  backups: { label: "Backups", emblem: Archive },
  data: { label: "Data", emblem: Database },
  db: { label: "Databases", emblem: Database },
  opt: { label: "Optional software", emblem: Box },
  srv: { label: "Served data", emblem: Servers },
  proc: { label: "Processes", emblem: Cpu },
  sys: { label: "Kernel objects", emblem: Cpu },
  ".trash": { label: "Trash", emblem: Trash },
  ".local": { label: "Local data" },
}

export function folderKind(name: string, path?: string): FolderKind {
  const named = FOLDERS_BY_NAME[name.toLowerCase()]
  if (named) return named
  // A folder directly under /home is somebody's home, whatever it is called.
  if (path && /^\/home\/[^/]+$/.test(path)) return { label: "Home folder", emblem: Home }
  return { label: "Folder" }
}

export function kindOfEntry(entry: Pick<FileEntry, "name" | "isDir"> & { path?: string }): {
  label: string
} {
  return entry.isDir ? folderKind(entry.name, entry.path) : fileKind(entry.name)
}

// ---- Drawing ---------------------------------------------------------------

// A 32-unit grid. The folder's tab sits behind its face; the page's corner is
// folded over at the top right, and the band runs along its foot.
const FOLDER_BACK =
  "M2.5 8A2.5 2.5 0 0 1 5 5.5H11.2A2 2 0 0 1 12.7 6.2L14.3 8A2 2 0 0 0 15.8 8.7H27A2.5 2.5 0 0 1 29.5 11.2V24.5A2.5 2.5 0 0 1 27 27H5A2.5 2.5 0 0 1 2.5 24.5Z"
const FOLDER_FRONT =
  "M2.5 13A2.5 2.5 0 0 1 5 10.5H27A2.5 2.5 0 0 1 29.5 13V24.5A2.5 2.5 0 0 1 27 27H5A2.5 2.5 0 0 1 2.5 24.5Z"
/** The face swung open: what the tree draws for the folder it has expanded. */
const FOLDER_OPEN =
  "M6.6 13.5H29.8A1.6 1.6 0 0 1 31.3 15.4L29.2 25.3A2.2 2.2 0 0 1 27 27H4.2A1.6 1.6 0 0 1 2.7 25.1L4.6 15A2 2 0 0 1 6.6 13.5Z"
const PAGE =
  "M8 2H19.5L26.5 9V27.5A2.5 2.5 0 0 1 24 30H8A2.5 2.5 0 0 1 5.5 27.5V4.5A2.5 2.5 0 0 1 8 2Z"
const FOLD = "M19.5 2V6.5A2.5 2.5 0 0 0 22 9H26.5Z"
const band = (top: number) => `M5.5 ${top}H26.5V27.5A2.5 2.5 0 0 1 24 30H8A2.5 2.5 0 0 1 5.5 27.5Z`
const TEXT_LINES = "M9.5 8.5H17M9.5 12H22.5M9.5 15.5H22.5M9.5 19H18.5"

/**
 * The icon for one entry.
 *
 * `detail` is for the sizes where a page is large enough to carry a word — a
 * tile, the viewer — and sets the extension on the band. In a row the band
 * is colour alone and the logo takes the room the word would have.
 */
export function FileIcon({
  entry,
  open,
  detail,
  className,
}: {
  entry: Pick<FileEntry, "name" | "isDir" | "isSymlink" | "linkBroken"> & { path?: string }
  /** Draw an opened folder — the row the tree has expanded. */
  open?: boolean
  detail?: boolean
  className?: string
}) {
  return (
    <span aria-hidden className={cn("relative inline-flex shrink-0", className)}>
      {entry.isDir ? (
        <FolderGlyph name={entry.name} path={entry.path} open={open} />
      ) : (
        <DocumentGlyph name={entry.name} detail={detail} />
      )}
      {entry.isSymlink && <AliasBadge broken={entry.linkBroken} />}
    </span>
  )
}

/** A folder alone, for a place that is not an entry: the strip, the sidebar. */
export function FolderIcon({
  name,
  path,
  className,
}: {
  name: string
  path?: string
  className?: string
}) {
  return (
    <span aria-hidden className={cn("relative inline-flex shrink-0", className)}>
      <FolderGlyph name={name} path={path} />
    </span>
  )
}

/** A folder face in one label's colour: the swatch a colour menu offers. */
export function FolderSwatch({ colour, className }: { colour: FolderColour; className?: string }) {
  return (
    <span
      aria-hidden
      data-folder=""
      className={cn("relative inline-flex shrink-0", className)}
      style={{ "--folder": `var(--folder-${colour})` } as CSSProperties}
    >
      <svg viewBox="0 0 32 32" className="size-full">
        <path d={FOLDER_BACK} fill="var(--folder-back)" />
        <path d={FOLDER_FRONT} fill="var(--folder)" />
      </svg>
    </span>
  )
}

function FolderGlyph({ name, path, open }: { name: string; path?: string; open?: boolean }) {
  const colour = useFolderColour(path, name)
  const { mark, emblem: Emblem } = folderKind(name, path)
  // Centred on the face, which sits lower once it has swung open.
  const place = cn(
    "absolute left-1/2 size-[30%] -translate-x-1/2 -translate-y-1/2",
    open ? "top-[64%]" : "top-[59%]",
  )
  return (
    <span
      data-folder=""
      className="relative block size-full"
      style={{ "--folder": `var(--folder-${colour})` } as CSSProperties}
    >
      <svg viewBox="0 0 32 32" className="size-full">
        <path d={FOLDER_BACK} fill="var(--folder-back)" />
        <path d={open ? FOLDER_OPEN : FOLDER_FRONT} fill="var(--folder)" />
      </svg>
      {mark ? (
        // A mask rather than an <img>: the mark is pressed into the face in
        // the face's own colour, a step darker, and Simple Icons draws every
        // product as one shape so its alpha is the whole of the drawing.
        <span
          className={place}
          style={{
            background: "var(--folder-mark)",
            WebkitMask: `url(/logos/mono/${mark}) center / contain no-repeat`,
            mask: `url(/logos/mono/${mark}) center / contain no-repeat`,
          }}
        />
      ) : Emblem ? (
        <Emblem className={place} style={{ color: "var(--folder-mark)" }} />
      ) : null}
    </span>
  )
}

function DocumentGlyph({ name, detail }: { name: string; detail?: boolean }) {
  const kind = fileKind(name)
  const Glyph = kind.glyph
  const word = kind.tag ?? kind.ext?.toUpperCase()
  const tag = detail && word && word.length <= 5 ? word : undefined
  // The band is a word's height on a tile and a stripe in a row, where the
  // mark gets the room back.
  const top = detail ? 21.5 : 23
  const mark = detail ? { x: 10.5, y: 7, size: 11 } : { x: 9.5, y: 6.5, size: 13 }
  return (
    <svg viewBox="0 0 32 32" className="size-full">
      <path d={PAGE} fill="var(--doc-page)" />
      <path d={FOLD} fill="var(--doc-fold)" />
      {kind.logo ? (
        <image
          href={`/logos/${kind.logo}`}
          x={mark.x}
          y={mark.y}
          width={mark.size}
          height={mark.size}
          preserveAspectRatio="xMidYMid meet"
        />
      ) : Glyph ? (
        <Glyph
          x={mark.x}
          y={mark.y}
          width={mark.size}
          height={mark.size}
          style={{ color: kind.tone }}
        />
      ) : (
        <path d={TEXT_LINES} stroke="var(--doc-line)" strokeWidth={1.5} strokeLinecap="round" />
      )}
      <path d={band(top)} fill={kind.tone} />
      {tag && (
        <text
          x={16}
          y={27.6}
          textAnchor="middle"
          fontSize={tag.length <= 3 ? 5.6 : tag.length === 4 ? 4.8 : 4}
          fontWeight={700}
          letterSpacing={0.2}
          fill="var(--doc-label)"
        >
          {tag}
        </text>
      )}
    </svg>
  )
}

/** The alias arrow: a link is its target's drawing with this in the corner. */
function AliasBadge({ broken }: { broken?: boolean }) {
  return (
    <svg viewBox="0 0 12 12" className="absolute -bottom-px -left-px size-[40%]">
      <rect
        x={0.5}
        y={0.5}
        width={11}
        height={11}
        rx={2.5}
        fill={broken ? "var(--destructive)" : "var(--doc-page)"}
        stroke="var(--background)"
        strokeWidth={0.75}
      />
      <path
        d="M3.5 8.8C3.5 6 5 4.6 8.3 4.6M6.4 2.8L8.3 4.6L6.4 6.4"
        fill="none"
        stroke={broken ? "var(--doc-label)" : "var(--background)"}
        strokeWidth={1.3}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

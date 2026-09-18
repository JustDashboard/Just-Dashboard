"use client"

import { forwardRef, type Ref } from "react"
import {
  mdiApplicationBrackets,
  mdiArchive,
  mdiBookOpenPageVariant,
  mdiCodeBraces,
  mdiCodeJson,
  mdiConsoleLine,
  mdiDatabase,
  mdiDocker,
  mdiFileCog,
  mdiFileCode,
  mdiFileDelimited,
  mdiFileDocument,
  mdiFileExcelBox,
  mdiFileGifBox,
  mdiFileImage,
  mdiFileJpgBox,
  mdiFileKey,
  mdiFileLock,
  mdiFileMusic,
  mdiFileOutline,
  mdiFilePdfBox,
  mdiFilePngBox,
  mdiFilePowerpointBox,
  mdiFileVideo,
  mdiFileWordBox,
  mdiFileXmlBox,
  mdiFolder,
  mdiFolderCog,
  mdiFolderHome,
  mdiFolderKey,
  mdiFolderMinus,
  mdiFolderNetwork,
  mdiFolderOpen,
  mdiFolderSync,
  mdiFormatFont,
  mdiGit,
  mdiHammerWrench,
  mdiLanguageCss3,
  mdiLanguageHtml5,
  mdiLanguageMarkdown,
  mdiLicense,
  mdiPackageVariantClosed,
  mdiSvg,
  mdiTextBox,
} from "@mdi/js"
import { Link, type Icon, type IconProps } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"

/**
 * One file glyph out of one MDI path.
 *
 * `@mdi/js` ships raw path data, not components, so this file wraps the paths
 * it needs in the same `Icon` shape the rest of the product uses: a 24-grid
 * glyph painting `currentColor`, defaulting to 16 unless a `size-*` class says
 * otherwise (CSS beats the width/height attributes below, so rows sizing with
 * `size-full` are untouched).
 */
function mdi(path: string, name: string): Icon {
  function MdiGlyph({ size = 16, title, ...props }: IconProps, ref: Ref<SVGSVGElement>) {
    return (
      <svg ref={ref} viewBox="0 0 24 24" fill="currentColor" width={size} height={size} {...props}>
        {title ? <title>{title}</title> : null}
        <path d={path} />
      </svg>
    )
  }
  const Forwarded = forwardRef<SVGSVGElement, Omit<IconProps, "ref">>(MdiGlyph)
  Forwarded.displayName = name
  return Forwarded
}
const MdiCode = mdi(mdiFileCode, "MdiCode")
const MdiHtml = mdi(mdiLanguageHtml5, "MdiHtml")
const MdiXml = mdi(mdiFileXmlBox, "MdiXml")
const MdiCss = mdi(mdiLanguageCss3, "MdiCss")
const MdiBraces = mdi(mdiCodeBraces, "MdiBraces")
const MdiJson = mdi(mdiCodeJson, "MdiJson")
const MdiDelimited = mdi(mdiFileDelimited, "MdiDelimited")
const MdiExcel = mdi(mdiFileExcelBox, "MdiExcel")
const MdiDatabase = mdi(mdiDatabase, "MdiDatabase")
const MdiDocument = mdi(mdiFileDocument, "MdiDocument")
const MdiPdf = mdi(mdiFilePdfBox, "MdiPdf")
const MdiWord = mdi(mdiFileWordBox, "MdiWord")
const MdiSlides = mdi(mdiFilePowerpointBox, "MdiSlides")
const MdiMarkdown = mdi(mdiLanguageMarkdown, "MdiMarkdown")
const MdiImage = mdi(mdiFileImage, "MdiImage")
const MdiPng = mdi(mdiFilePngBox, "MdiPng")
const MdiJpg = mdi(mdiFileJpgBox, "MdiJpg")
const MdiGif = mdi(mdiFileGifBox, "MdiGif")
const MdiSvg = mdi(mdiSvg, "MdiSvg")
const MdiVideo = mdi(mdiFileVideo, "MdiVideo")
const MdiAudio = mdi(mdiFileMusic, "MdiAudio")
const MdiArchive = mdi(mdiArchive, "MdiArchive")
const MdiConsole = mdi(mdiConsoleLine, "MdiConsole")
const MdiCog = mdi(mdiFileCog, "MdiCog")
const MdiKeyFile = mdi(mdiFileKey, "MdiKeyFile")
const MdiBinary = mdi(mdiApplicationBrackets, "MdiBinary")
const MdiFont = mdi(mdiFormatFont, "MdiFont")
const MdiLog = mdi(mdiTextBox, "MdiLog")
const MdiPackage = mdi(mdiPackageVariantClosed, "MdiPackage")
const MdiDocker = mdi(mdiDocker, "MdiDocker")
const MdiPlain = mdi(mdiFileOutline, "MdiPlain")
const MdiLock = mdi(mdiFileLock, "MdiLock")
const MdiMake = mdi(mdiHammerWrench, "MdiMake")
const MdiReadme = mdi(mdiBookOpenPageVariant, "MdiReadme")
const MdiLicence = mdi(mdiLicense, "MdiLicence")
const MdiGit = mdi(mdiGit, "MdiGit")
const MdiFolder = mdi(mdiFolder, "MdiFolder")
const MdiFolderOpen = mdi(mdiFolderOpen, "MdiFolderOpen")
const MdiFolderMinus = mdi(mdiFolderMinus, "MdiFolderMinus")
const MdiFolderHome = mdi(mdiFolderHome, "MdiFolderHome")
const MdiFolderCog = mdi(mdiFolderCog, "MdiFolderCog")
const MdiFolderKey = mdi(mdiFolderKey, "MdiFolderKey")
const MdiFolderNetwork = mdi(mdiFolderNetwork, "MdiFolderNetwork")
const MdiFolderSync = mdi(mdiFolderSync, "MdiFolderSync")

/**
 * What a file *is*, drawn.
 *
 * Every row in this page used to carry one of three glyphs — folder, link, or
 * the generic sheet of paper — which meant a directory of forty files was
 * forty identical icons and the only way to tell a config from a certificate
 * from a database dump was to read the extension off the end of the name. An
 * icon that says nothing is worse than no icon: it costs the same space and
 * trains you to ignore the column.
 *
 * The colours are the shared semantic `--tag-*` tokens rather than anything
 * computed from the palette, for the same reason they are there: a category
 * is a label, and a label whose hue changes with the theme stops being the
 * same label. They are fixed hues that hold up on a near-black card and a
 * near-white one alike.
 *
 * The mapping is deliberately by *category* rather than by language. Twenty
 * distinct icons is a legend to memorise; eight categories with one hue each
 * is something the eye picks up in a directory or two — code is blue, data is
 * amber, media is pink, keys and certificates are green because they are the
 * ones you must not paste into a chat window.
 *
 * **The glyphs are Material Design Icons' file family, and that is what lets
 * the mapping be both categorical and literal.** A general UI set has no `JSON`
 * or `JPG` to draw; MDI's file boxes spell the format on the sheet (`PDF`,
 * `XLS`, `JPG`, `GIF`), so a badge that says what it is needs nothing
 * memorised. They keep their category's hue, so the colour system is untouched
 * and a `.csv` is still the green of tabular data. Source stays one glyph in
 * code blue whatever language it is written in — per-language logos would be a
 * second legend on top of the categories.
 *
 * The symlink corner badge is the one glyph here that is not MDI: it is the
 * product's own `Link` from the Heroicons vocabulary in `components/icons.tsx`,
 * because it marks a filesystem relation rather than a file type.
 */
export type FileTone =
  "slate" | "red" | "amber" | "green" | "cyan" | "blue" | "violet" | "pink" | "primary"

export type FileKind = {
  icon: Icon
  tone: FileTone
  /** What this is, in the words somebody would use out loud. */
  label: string
}

const CODE: FileKind = { icon: MdiCode, tone: "blue", label: "Source code" }
const HTML: FileKind = { icon: MdiHtml, tone: "violet", label: "Markup" }
const XML: FileKind = { icon: MdiXml, tone: "violet", label: "Markup" }
const STYLE: FileKind = { icon: MdiCss, tone: "violet", label: "Stylesheet" }
const DATA: FileKind = { icon: MdiBraces, tone: "amber", label: "Structured data" }
const JSON_: FileKind = { icon: MdiJson, tone: "amber", label: "JSON" }
const SHEET: FileKind = { icon: MdiDelimited, tone: "green", label: "Tabular data" }
const XLS: FileKind = { icon: MdiExcel, tone: "green", label: "Spreadsheet" }
const SQL: FileKind = { icon: MdiDatabase, tone: "amber", label: "SQL" }
const DOC: FileKind = { icon: MdiDocument, tone: "slate", label: "Document" }
const PDF: FileKind = { icon: MdiPdf, tone: "slate", label: "PDF" }
const WORD: FileKind = { icon: MdiWord, tone: "slate", label: "Word document" }
const SLIDES: FileKind = { icon: MdiSlides, tone: "slate", label: "Presentation" }
const MARKDOWN: FileKind = { icon: MdiMarkdown, tone: "slate", label: "Markdown" }
const IMAGE: FileKind = { icon: MdiImage, tone: "pink", label: "Image" }
const PNG: FileKind = { icon: MdiPng, tone: "pink", label: "Image" }
const SVG: FileKind = { icon: MdiSvg, tone: "pink", label: "Vector image" }
const JPEG: FileKind = { icon: MdiJpg, tone: "pink", label: "Image" }
const GIF: FileKind = { icon: MdiGif, tone: "pink", label: "Animation" }
const VIDEO: FileKind = { icon: MdiVideo, tone: "violet", label: "Video" }
const AUDIO: FileKind = { icon: MdiAudio, tone: "cyan", label: "Audio" }
const ARCHIVE: FileKind = { icon: MdiArchive, tone: "amber", label: "Archive" }
const SHELL: FileKind = { icon: MdiConsole, tone: "green", label: "Shell script" }
const CONFIG: FileKind = { icon: MdiCog, tone: "cyan", label: "Configuration" }
const SECRET: FileKind = { icon: MdiKeyFile, tone: "green", label: "Key or certificate" }
const BINARY: FileKind = { icon: MdiBinary, tone: "red", label: "Binary" }
const FONT: FileKind = { icon: MdiFont, tone: "violet", label: "Font" }
const LOG: FileKind = { icon: MdiLog, tone: "amber", label: "Log" }
const PACKAGE: FileKind = { icon: MdiPackage, tone: "red", label: "Package" }
const DOCKER: FileKind = { icon: MdiDocker, tone: "blue", label: "Container build" }
const PLAIN: FileKind = { icon: MdiPlain, tone: "slate", label: "File" }

const BY_EXTENSION: Record<string, FileKind> = {
  // Code
  go: CODE,
  rs: CODE,
  py: CODE,
  rb: CODE,
  php: CODE,
  java: CODE,
  kt: CODE,
  swift: CODE,
  c: CODE,
  h: CODE,
  cpp: CODE,
  cc: CODE,
  hpp: CODE,
  cs: CODE,
  ts: CODE,
  tsx: CODE,
  js: CODE,
  jsx: CODE,
  mjs: CODE,
  cjs: CODE,
  mts: CODE,
  lua: CODE,
  pl: CODE,
  ex: CODE,
  exs: CODE,
  erl: CODE,
  hs: CODE,
  scala: CODE,
  clj: CODE,
  dart: CODE,
  r: CODE,
  zig: CODE,
  vue: CODE,
  svelte: CODE,
  astro: CODE,
  // Shell and automation
  sh: SHELL,
  bash: SHELL,
  zsh: SHELL,
  fish: SHELL,
  ps1: SHELL,
  bat: SHELL,
  cmd: SHELL,
  // Markup and style
  html: HTML,
  htm: HTML,
  xml: XML,
  svg: SVG,
  css: STYLE,
  scss: STYLE,
  sass: STYLE,
  less: STYLE,
  // Data
  json: JSON_,
  jsonc: JSON_,
  json5: JSON_,
  yaml: DATA,
  yml: DATA,
  toml: DATA,
  proto: DATA,
  graphql: DATA,
  gql: DATA,
  ndjson: JSON_,
  csv: SHEET,
  tsv: SHEET,
  ods: SHEET,
  xlsx: XLS,
  xls: XLS,
  sql: SQL,
  db: SQL,
  sqlite: SQL,
  sqlite3: SQL,
  dump: SQL,
  // Documents
  md: MARKDOWN,
  mdx: MARKDOWN,
  txt: DOC,
  rst: DOC,
  adoc: DOC,
  pdf: PDF,
  doc: WORD,
  docx: WORD,
  ppt: SLIDES,
  pptx: SLIDES,
  odp: SLIDES,
  log: LOG,
  // Media
  png: PNG,
  jpg: JPEG,
  jpeg: JPEG,
  gif: GIF,
  webp: IMAGE,
  avif: IMAGE,
  bmp: IMAGE,
  ico: IMAGE,
  tiff: IMAGE,
  heic: IMAGE,
  psd: IMAGE,
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
  woff: FONT,
  woff2: FONT,
  ttf: FONT,
  otf: FONT,
  eot: FONT,
  // Archives and packages
  zip: ARCHIVE,
  tar: ARCHIVE,
  gz: ARCHIVE,
  tgz: ARCHIVE,
  bz2: ARCHIVE,
  xz: ARCHIVE,
  zst: ARCHIVE,
  "7z": ARCHIVE,
  rar: ARCHIVE,
  jar: ARCHIVE,
  deb: PACKAGE,
  rpm: PACKAGE,
  apk: PACKAGE,
  whl: PACKAGE,
  iso: PACKAGE,
  // Configuration
  conf: CONFIG,
  cfg: CONFIG,
  ini: CONFIG,
  env: CONFIG,
  properties: CONFIG,
  service: CONFIG,
  socket: CONFIG,
  timer: CONFIG,
  mount: CONFIG,
  rules: CONFIG,
  tf: CONFIG,
  tfvars: CONFIG,
  hcl: CONFIG,
  nginx: CONFIG,
  list: CONFIG,
  // Secrets
  pem: SECRET,
  key: SECRET,
  crt: SECRET,
  cer: SECRET,
  csr: SECRET,
  p12: SECRET,
  pfx: SECRET,
  pub: SECRET,
  gpg: SECRET,
  asc: SECRET,
  kdbx: SECRET,
  // Binary
  so: BINARY,
  o: BINARY,
  a: BINARY,
  dll: BINARY,
  exe: BINARY,
  bin: BINARY,
  dat: BINARY,
  pyc: BINARY,
  wasm: BINARY,
  img: BINARY,
  swp: BINARY,
}

/** Files a server keeps that have no extension to key off at all. */
const BY_NAME: Record<string, FileKind> = {
  dockerfile: DOCKER,
  containerfile: DOCKER,
  "docker-compose.yml": DOCKER,
  "docker-compose.yaml": DOCKER,
  "compose.yml": DOCKER,
  "compose.yaml": DOCKER,
  ".dockerignore": DOCKER,
  makefile: { icon: MdiMake, tone: "cyan", label: "Makefile" },
  gnumakefile: { icon: MdiMake, tone: "cyan", label: "Makefile" },
  caddyfile: CONFIG,
  vagrantfile: CONFIG,
  procfile: CONFIG,
  gemfile: CODE,
  rakefile: CODE,
  "package.json": { icon: MdiPackage, tone: "red", label: "npm manifest" },
  "package-lock.json": { icon: MdiLock, tone: "slate", label: "Lockfile" },
  "bun.lock": { icon: MdiLock, tone: "slate", label: "Lockfile" },
  "bun.lockb": { icon: MdiLock, tone: "slate", label: "Lockfile" },
  "yarn.lock": { icon: MdiLock, tone: "slate", label: "Lockfile" },
  "pnpm-lock.yaml": { icon: MdiLock, tone: "slate", label: "Lockfile" },
  "go.sum": { icon: MdiLock, tone: "slate", label: "Lockfile" },
  "cargo.lock": { icon: MdiLock, tone: "slate", label: "Lockfile" },
  "go.mod": { icon: MdiPackage, tone: "cyan", label: "Go module" },
  "cargo.toml": { icon: MdiPackage, tone: "red", label: "Cargo manifest" },
  "requirements.txt": { icon: MdiPackage, tone: "blue", label: "Python requirements" },
  license: { icon: MdiLicence, tone: "slate", label: "Licence" },
  "license.md": { icon: MdiLicence, tone: "slate", label: "Licence" },
  readme: { icon: MdiReadme, tone: "cyan", label: "Readme" },
  "readme.md": { icon: MdiMarkdown, tone: "cyan", label: "Readme" },
  changelog: DOC,
  "changelog.md": MARKDOWN,
  ".gitignore": { icon: MdiGit, tone: "slate", label: "git exclusions" },
  ".gitconfig": CONFIG,
  ".env": { icon: MdiKeyFile, tone: "amber", label: "Environment file" },
  ".bashrc": SHELL,
  ".zshrc": SHELL,
  ".profile": SHELL,
  ".bash_profile": SHELL,
  ".editorconfig": CONFIG,
  authorized_keys: SECRET,
  known_hosts: SECRET,
  passwd: { icon: MdiKeyFile, tone: "red", label: "Account database" },
  shadow: { icon: MdiKeyFile, tone: "red", label: "Password hashes" },
  fstab: CONFIG,
  hosts: CONFIG,
  crontab: CONFIG,
}

/**
 * Folders whose name says more than "folder" does.
 *
 * The build and dependency directories take the minus folder rather than a
 * folder-with-a-gear: what they have in common is not that they are
 * configured, it is that nothing in them is yours to edit, and a folder with
 * a minus in it says "walk past this one" at a glance.
 */
const FOLDERS_BY_NAME: Record<string, FileKind> = {
  ".git": { icon: MdiGit, tone: "amber", label: "git repository" },
  node_modules: { icon: MdiFolderMinus, tone: "slate", label: "Installed packages" },
  vendor: { icon: MdiFolderMinus, tone: "slate", label: "Vendored dependencies" },
  ".next": { icon: MdiFolderMinus, tone: "slate", label: "Build output" },
  dist: { icon: MdiFolderMinus, tone: "slate", label: "Build output" },
  build: { icon: MdiFolderMinus, tone: "slate", label: "Build output" },
  target: { icon: MdiFolderMinus, tone: "slate", label: "Build output" },
  etc: { icon: MdiFolderCog, tone: "cyan", label: "Configuration" },
  home: { icon: MdiFolderHome, tone: "primary", label: "Home directories" },
  root: { icon: MdiFolderHome, tone: "primary", label: "root's home" },
  var: { icon: MdiFolder, tone: "amber", label: "Variable data" },
  log: { icon: MdiFolder, tone: "amber", label: "Logs" },
  logs: { icon: MdiFolder, tone: "amber", label: "Logs" },
  www: { icon: MdiFolderNetwork, tone: "violet", label: "Web root" },
  public: { icon: MdiFolderNetwork, tone: "violet", label: "Public assets" },
  ssl: { icon: MdiFolderKey, tone: "green", label: "Certificates" },
  ssh: { icon: MdiFolderKey, tone: "green", label: "SSH configuration" },
  ".ssh": { icon: MdiFolderKey, tone: "green", label: "SSH keys" },
  backups: { icon: MdiFolderSync, tone: "amber", label: "Backups" },
}

const TONE_VAR: Record<FileTone, string> = {
  slate: "var(--tag-slate)",
  red: "var(--tag-red)",
  amber: "var(--tag-amber)",
  green: "var(--tag-green)",
  cyan: "var(--tag-cyan)",
  blue: "var(--tag-blue)",
  violet: "var(--tag-violet)",
  pink: "var(--tag-pink)",
  primary: "var(--primary)",
}

/**
 * The kind of a name.
 *
 * Order matters: the whole name first (a `docker-compose.yml` is not merely
 * YAML), then the extension, then the extension *under* a backup suffix — an
 * `nginx.conf.bak` is still a config file, and the pass that forgets this is
 * how a directory of saved configs turns into a wall of blank sheets.
 */
export function fileKind(name: string, isDir = false): FileKind {
  const lower = name.toLowerCase()
  if (isDir) {
    return FOLDERS_BY_NAME[lower] ?? { icon: MdiFolder, tone: "primary", label: "Folder" }
  }
  if (BY_NAME[lower]) return BY_NAME[lower]

  const parts = lower.split(".")
  if (parts.length > 1) {
    const ext = parts[parts.length - 1]
    if (BY_EXTENSION[ext]) return BY_EXTENSION[ext]
    if (parts.length > 2 && BACKUP_SUFFIXES.has(ext)) {
      const under = BY_EXTENSION[parts[parts.length - 2]]
      if (under) return under
    }
    // `.tar.gz` and friends: the archive is the pair, not the last word.
    if (parts.length > 2 && parts[parts.length - 2] === "tar") return ARCHIVE
  }
  return PLAIN
}

const BACKUP_SUFFIXES = new Set(["bak", "old", "orig", "save", "disabled", "dpkg-old", "rpmsave"])

export function kindOfEntry(entry: Pick<FileEntry, "name" | "isDir">): FileKind {
  return fileKind(entry.name, entry.isDir)
}

/** The CSS colour for a kind, for anything that draws its own glyph. */
export function toneColour(tone: FileTone): string {
  return TONE_VAR[tone]
}

/**
 * The icon for one entry.
 *
 * A symlink keeps its target's icon and gains the link glyph as a corner
 * badge rather than replacing it: what a link points at is the useful fact,
 * and the old listing threw it away to draw a chain on every one of them.
 *
 * **The colour is a fallback, not a fixed value.** A row that selects itself
 * with a solid `bg-primary` — the places rail does — is white in dark mode and
 * black in light, and a tone painted straight onto the glyph is then a folder
 * the same colour as the thing behind it: `primary` on `bg-primary` is
 * invisible in *both* themes, which is the one way to get it wrong twice. The
 * tone is therefore the fallback of `--file-icon-colour`, so any ancestor can
 * say "in here, icons are my foreground" by setting that one property. It has
 * to be a custom property rather than a class because this is an inline style,
 * and nothing but inline beats inline.
 *
 * A directory used to be tinted with `fill-current/15` to set it apart from a
 * file. That worked on a stroked set, whose paths carry no fill of their own
 * and so inherit one; every path in this set paints itself `currentColor`, so
 * the class is now a no-op that only looks like it does something. It is gone,
 * and nothing replaces it — the MDI folder is already a different silhouette
 * from every file glyph, which is the distinction the tint was standing in
 * for.
 */
export function FileIcon({
  entry,
  open,
  className,
  badgeClassName,
}: {
  entry: Pick<FileEntry, "name" | "isDir" | "isSymlink" | "linkBroken">
  /** Draw an opened folder — the row the listing is currently inside. */
  open?: boolean
  className?: string
  badgeClassName?: string
}) {
  const kind = kindOfEntry(entry)
  const Glyph = entry.isDir && open ? MdiFolderOpen : kind.icon
  return (
    <span className={cn("relative inline-flex shrink-0", className)}>
      <Glyph
        className="size-full"
        style={{
          color: `var(--file-icon-colour, ${
            entry.linkBroken ? "var(--tag-red)" : TONE_VAR[kind.tone]
          })`,
        }}
        aria-hidden
      />
      {entry.isSymlink && (
        <Link
          className={cn(
            "absolute -right-0.5 -bottom-0.5 size-1/2 rounded-[2px] bg-card text-muted-foreground",
            badgeClassName,
          )}
          aria-hidden
        />
      )}
    </span>
  )
}

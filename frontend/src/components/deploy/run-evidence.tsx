"use client"

import { Fragment } from "react"
import {
  Archive,
  Bell,
  Box,
  Check,
  Copy,
  Crosshair,
  GitCommit,
  GitTag,
  Globe,
  Heart,
  Layers,
  LockClosed,
  Route,
  ShieldCheck,
  StopCircle,
  Terminal,
  type Icon,
} from "@/components/icons"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { tokenClass } from "@/components/logs/log-text"
import { cn } from "@/lib/utils"
import type { DeploymentStep, DeploymentSummary } from "@/lib/types"
import { GroupRule } from "@/components/flow"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { AuthorMark, ShortSha } from "@/components/git/marks"
import {
  ProductGlyph,
  ProductLogo,
  buildMethodProduct,
  frameworkProduct,
  hasProductLogo,
  imageProduct,
  issuerProduct,
  packageManagerProduct,
  recipeProduct,
} from "@/components/product-logo"
import {
  StepMark,
  formatDuration,
  frameworkLabel,
  sourceProduct,
  type ReleaseNodeState,
} from "@/components/deploy/vocabulary"

/**
 * What a run's steps recorded, drawn as what each thing is.
 *
 * The engine keeps a step's evidence as JSON, and the run page used to print
 * it that way: an accordion row opened onto a well of grey braces, so the
 * commit a build checked out, the Dockerfile it wrote and the image it made
 * were all found by reading punctuation. Here the same record is read by
 * shape — a value by its key, a list by what its items carry — and drawn the
 * way the rest of the product draws it: a product as its logo (§14), a commit
 * as the Git page draws one, a digest cut short with the whole of it a copy
 * away, an image as its product beside its size and platform, a check as its
 * outcome. What has no better shape — a Dockerfile, a command line, a nested
 * record — is a code block in the log console's token hues, and the record
 * as it was kept stays one fold away.
 */

type Json = Record<string, unknown>

function isRecord(value: unknown): value is Json {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

function isPrimitive(value: unknown): value is string | number | boolean {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean"
}

function text(value: unknown) {
  return typeof value === "string" && value !== "" ? value : undefined
}

/** Go's zero `time.Time`, which an `omitempty` on a struct field still emits. */
const ZERO_TIME = /^0001-01-01T/

/** A value worth a row: not empty, not Go's zero time, not an empty list or record. */
function present(value: unknown): boolean {
  if (value === null || value === undefined || value === "") return false
  if (typeof value === "string" && ZERO_TIME.test(value)) return false
  if (Array.isArray(value)) return value.length > 0
  if (isRecord(value)) return Object.values(value).some(present)
  return true
}

/** "routeRemoved" → "Route removed", "release_id" → "Release id". */
function factLabel(key: string) {
  const words = key
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[_-]+/g, " ")
    .toLowerCase()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

const DIGEST = /^sha256:[0-9a-f]{12,}$/
const HEX = /^[0-9a-f]{12,64}$/
const PATH = /^(\/|\.\/|\.just-dashboard\/)/
const URL_TEXT = /^https?:\/\//
const ISO_TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/

/** A content digest or a full commit hash: cut short where it is drawn. */
function isDigest(value: string) {
  return DIGEST.test(value) || (HEX.test(value) && value.length >= 32)
}

/**
 * How a step is drawn: the product it works with, where that is known and
 * not a guess, else its glyph on the same tile. Fetching is the forge the
 * source lives on, preparing is the toolchain the recipe chose, building and
 * starting are the container runtime, issuing a certificate is Let's
 * Encrypt — the authority certbot asks, as the proxy's own Issue certificate
 * draws it — and a step that is the dashboard's own bookkeeping keeps a glyph.
 */
const STEP_GLYPH: Record<string, Icon> = {
  resolve_source: GitCommit,
  acquire_source: GitCommit,
  analyze_plan: ShieldCheck,
  prepare_context: Box,
  build_artifact: Box,
  render_runtime: Layers,
  backup_gate: Archive,
  release_task: Terminal,
  provision_certificate: LockClosed,
  start_candidate: Box,
  verify_readiness: Heart,
  verify_smoke: Crosshair,
  activate: Route,
  retire_previous: StopCircle,
  record_release: GitTag,
  notify: Bell,
}

const RUNTIME_PRODUCT: Record<string, string> = {
  container: "docker",
  compose: "docker-compose",
  pm2: "pm2",
}

/** The package manager a toolchain line names first: "bun 1.3.11 (packageManager)" is Bun. */
function toolchainProduct(toolchain: unknown) {
  const word = text(toolchain)?.split(/\s+/)[0]?.toLowerCase()
  if (!word) return undefined
  if (word === "node") return "nodejs"
  return hasProductLogo(word) ? word : undefined
}

function stepProduct(
  step: Pick<DeploymentStep, "key" | "evidence">,
  deployment?: DeploymentSummary,
): string | undefined {
  const evidence = step.evidence ?? {}
  switch (step.key) {
    case "resolve_source":
    case "acquire_source":
      return deployment ? sourceProduct(deployment) : undefined
    case "analyze_plan": {
      const candidate = isRecord(evidence.candidate) ? evidence.candidate : undefined
      return (
        frameworkProduct(text(candidate?.framework)) ??
        recipeProduct(text(candidate?.recipe), text(candidate?.packageManager))
      )
    }
    case "prepare_context": {
      const prepared = isRecord(evidence.prepared) ? evidence.prepared : undefined
      return (
        toolchainProduct(prepared?.toolchain) ??
        buildMethodProduct(text(prepared?.method) ?? deployment?.buildMethod, {
          recipe: text(prepared?.recipe) ?? deployment?.recipe,
        })
      )
    }
    case "build_artifact":
      return deployment?.buildMethod === "compose" ? "docker-compose" : "docker"
    case "start_candidate":
    case "retire_previous": {
      const runtime = isRecord(evidence.runtime) ? text(evidence.runtime.kind) : undefined
      return (
        (runtime && RUNTIME_PRODUCT[runtime]) ??
        (deployment?.buildMethod === "compose" ? "docker-compose" : "docker")
      )
    }
    case "provision_certificate":
      return "lets-encrypt"
    default:
      return undefined
  }
}

/**
 * A step on its tile, with how it went in the tile's corner — the way a
 * project's favicon carries its framework (`ProjectMark`). A step the run has
 * not reached is drawn a step back.
 */
export function StepTile({
  step,
  state,
  deployment,
  size = "sm",
  className,
}: {
  step: Pick<DeploymentStep, "key" | "evidence">
  state: ReleaseNodeState
  deployment?: DeploymentSummary
  size?: "sm" | "md"
  className?: string
}) {
  return (
    <span aria-hidden className={cn("relative flex shrink-0 self-start", className)}>
      <ProductLogo
        id={stepProduct(step, deployment)}
        fallback={STEP_GLYPH[step.key] ?? Box}
        size={size}
        className={cn(
          state === "pending" && "opacity-45",
          (state === "failed" || state === "blocked") && "border-rule-danger",
          state === "running" && "border-rule-brand",
        )}
      />
      {state !== "pending" && (
        <span className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-sm border border-hairline bg-background">
          <StepMark state={state} className="size-3 [&_svg]:size-3" />
        </span>
      )}
    </span>
  )
}

/**
 * The one line a step's row shows while another step is open: what it
 * concluded, from what it recorded — the commit it checked out, the image it
 * made and how large, the names a certificate covers — else the reason it
 * gave, else nothing.
 */
export function stepSummary(step: DeploymentStep): string | undefined {
  if (step.errorMessage) return step.errorMessage
  const evidence = step.evidence ?? {}
  const reason = text(evidence.reason)
  if (reason) return reason
  switch (step.key) {
    case "resolve_source": {
      const revision = text(evidence.revision)
      return revision ? `${text(evidence.kind) ?? "source"} at ${shortHash(revision)}` : undefined
    }
    case "acquire_source": {
      const commit = isRecord(evidence.commit) ? evidence.commit : undefined
      return text(commit?.subject) ?? (text(evidence.revision) && "checked out")
    }
    case "analyze_plan": {
      const findings = Array.isArray(evidence.findings) ? (evidence.findings as Json[]) : []
      const open = findings.filter((finding) => finding.severity !== "pass").length
      const candidate = isRecord(evidence.candidate) ? evidence.candidate : undefined
      const framework = text(candidate?.framework)
      return [
        framework && frameworkLabel(framework),
        findings.length > 0 &&
          (open > 0 ? plural(open, "finding") : `${plural(findings.length, "check")} passed`),
      ]
        .filter(Boolean)
        .join(" · ")
    }
    case "prepare_context": {
      const prepared = isRecord(evidence.prepared) ? evidence.prepared : undefined
      const node = text(prepared?.nodeVersion)?.split(" ")[0]
      const toolchain = text(prepared?.toolchain)?.split(" (")[0]
      return [node && `Node ${node}`, toolchain].filter(Boolean).join(" · ") || undefined
    }
    case "build_artifact": {
      const result = isRecord(evidence.result) ? evidence.result : undefined
      const artifact = Array.isArray(result?.artifacts) ? (result.artifacts[0] as Json) : undefined
      if (!artifact) return undefined
      const metadata = isRecord(artifact.metadata) ? artifact.metadata : {}
      const platform = Array.isArray(metadata.platforms) ? text(metadata.platforms[0]) : undefined
      const size = typeof artifact.sizeBytes === "number" ? bytes(artifact.sizeBytes) : undefined
      return [text(artifact.kind), size, platform].filter(Boolean).join(" · ")
    }
    case "render_runtime":
    case "record_release":
      return typeof evidence.releaseNumber === "number"
        ? `release #${evidence.releaseNumber}`
        : undefined
    case "provision_certificate": {
      const domains = Array.isArray(evidence.domains) ? evidence.domains.map(String) : []
      const outcome = text(evidence.outcome)
      return [domains.join(", "), outcome && outcome !== "failed" && outcome]
        .filter(Boolean)
        .join(" · ")
    }
    case "start_candidate": {
      const runtime = isRecord(evidence.runtime) ? evidence.runtime : undefined
      const name = text(runtime?.name)
      return name ? `${name}${runtime?.port ? ` on port ${runtime.port}` : ""}` : undefined
    }
    case "verify_readiness":
    case "verify_smoke": {
      const health = isRecord(evidence.health) ? evidence.health : evidence
      const checks = Array.isArray(health.checks) ? (health.checks as Json[]) : []
      if (checks.length === 0) return undefined
      const passed = checks.filter((check) => check.outcome === "passed").length
      return `${passed} of ${plural(checks.length, "check")} passed`
    }
    case "activate": {
      const route = isRecord(evidence.route) ? evidence.route : undefined
      return text(route?.name)
    }
    case "notify":
      return typeof evidence.channels === "number"
        ? plural(evidence.channels, "channel")
        : undefined
    default:
      return undefined
  }
}

function shortHash(value: string) {
  return value.replace(/^sha256:/, "").slice(0, 7)
}

/**
 * A step's record, read by shape. A wrapper with one key — `{result: …}` — is
 * opened, so the reader is not handed a section heading with one section
 * under it. `omit` names what the caller already drew another way.
 */
export function EvidenceView({ evidence, omit = [] }: { evidence: Json; omit?: string[] }) {
  let record = evidence
  while (true) {
    const keys = Object.keys(record).filter((key) => !omit.includes(key))
    const only = keys.length === 1 ? record[keys[0]] : undefined
    if (!isRecord(only)) break
    record = only
  }
  const entries = Object.entries(record).filter(
    ([key, value]) => !omit.includes(key) && present(value),
  )
  if (entries.length === 0) return null
  return <Block entries={entries} depth={0} />
}

/** One level of a record: its plain facts as a grid, then everything that has a shape of its own. */
function Block({ entries, depth }: { entries: [string, unknown][]; depth: number }) {
  const facts: [string, unknown][] = []
  const shaped: React.ReactNode[] = []
  for (const [key, value] of entries) {
    if (typeof value === "string" && value.includes("\n")) {
      shaped.push(<TextBlock key={key} name={key} value={value} />)
    } else if (isPrimitive(value) || (key === "commit" && isRecord(value))) {
      facts.push([key, value])
    } else if (Array.isArray(value) && value.every(isPrimitive)) {
      if (/argv$/i.test(key)) shaped.push(<CommandBlock key={key} argv={value.map(String)} />)
      else facts.push([key, value])
    } else if (Array.isArray(value)) {
      shaped.push(<ObjectList key={key} name={key} items={value.filter(isRecord)} depth={depth} />)
    } else if (isRecord(value)) {
      const inner = Object.entries(value).filter(([, item]) => present(item))
      shaped.push(
        depth >= 2 ? (
          <CodeBlock key={key} title={factLabel(key)} copy={JSON.stringify(value, null, 2)}>
            <JsonCode value={value} />
          </CodeBlock>
        ) : (
          <section key={key} aria-label={factLabel(key)} className="min-w-0 space-y-3">
            <GroupRule label={factLabel(key)} />
            <Block entries={inner} depth={depth + 1} />
          </section>
        ),
      )
    }
  }
  return (
    <div className="min-w-0 space-y-5">
      {facts.length > 0 && <FactGrid facts={facts} />}
      {shaped}
    </div>
  )
}

function FactGrid({ facts }: { facts: [string, unknown][] }) {
  return (
    <dl className="grid min-w-0 grid-cols-[repeat(auto-fill,minmax(11rem,1fr))] gap-x-6 gap-y-3.5">
      {facts.map(([key, value]) => (
        <div
          key={key}
          className={cn(
            "min-w-0",
            // A path, a digest or a list reads on one line or not at all.
            ((typeof value === "string" && value.length > 28 && !isDigest(value)) ||
              (Array.isArray(value) && value.length > 2) ||
              key === "commit") &&
              "col-span-full",
          )}
        >
          <dt className="text-hint text-muted-foreground">{factLabel(key)}</dt>
          <dd className="mt-1 flex min-w-0 flex-wrap items-center gap-1.5 text-body">
            <FactValue name={key} value={value} />
          </dd>
        </div>
      ))}
    </dl>
  )
}

/**
 * The product a fact names, by its key — a Node version is Node's, a
 * toolchain its package manager's, a platform Linux's — and never by a guess
 * at a value no key explains.
 */
function factProduct(key: string, value: string): string | undefined {
  switch (key) {
    case "recipe":
      return recipeProduct(value)
    case "method":
    case "buildMethod":
      return buildMethodProduct(value)
    case "framework":
      return frameworkProduct(value)
    case "packageManager":
      return packageManagerProduct(value)
    case "toolchain":
      return toolchainProduct(value)
    case "nodeVersion":
      return "nodejs"
    case "goVersion":
      return value ? "go" : undefined
    case "pythonVersion":
      return "python"
    case "phpVersion":
      return "php"
    case "javaVersion":
      return "java"
    case "dotnetVersion":
      return "dotnet"
    case "reference":
    case "image":
      return imageProduct(value)
    case "os":
    case "targetPlatform":
      return value.startsWith("linux") ? "linux" : undefined
    case "issuer":
      return issuerProduct(value)
    case "kind":
      return value === "git" ? "git" : value === "compose" ? "docker-compose" : undefined
    default:
      return undefined
  }
}

function FactValue({ name, value }: { name: string; value: unknown }) {
  if (name === "commit" && isRecord(value)) return <CommitValue commit={value} />
  if (Array.isArray(value)) {
    const host = name === "domains" || name === "names"
    return (
      <span className="flex min-w-0 flex-wrap gap-1.5">
        {value.map((item, index) => (
          <Tag key={index} mono className="max-w-full text-xs">
            {host && <Globe aria-hidden className="size-3 shrink-0 text-muted-foreground" />}
            <span className="truncate">{String(item)}</span>
          </Tag>
        ))}
      </span>
    )
  }
  if (typeof value === "boolean")
    return value ? (
      <>
        <Check aria-hidden className="size-3.5 text-success" />
        Yes
      </>
    ) : (
      <span className="text-muted-foreground">No</span>
    )
  if (typeof value === "number") return <span className="numeric">{numberText(name, value)}</span>
  const string = String(value)
  if (isDigest(string)) return <Digest value={string} label={factLabel(name)} />
  if (ISO_TIME.test(string))
    return (
      <>
        <time dateTime={string}>{timestamp(string)}</time>
        <span className="text-hint text-muted-foreground">{relativeTime(string)}</span>
      </>
    )
  if (PATH.test(string) || URL_TEXT.test(string))
    return (
      <span className={cn("min-w-0 font-mono text-xs wrap-anywhere", tokenClass("path"))}>
        {string}
      </span>
    )
  const product = factProduct(name, string)
  return (
    <>
      {product && hasProductLogo(product) && <ProductGlyph id={product} />}
      <span className="min-w-0 wrap-anywhere">{string}</span>
    </>
  )
}

function numberText(name: string, value: number) {
  if (/bytes$/i.test(name)) return bytes(value)
  if (/millis$|ms$/i.test(name)) return formatDuration(value / 1000)
  if (/seconds$/i.test(name)) return formatDuration(value)
  if (name === "releaseNumber") return `#${value}`
  return value.toLocaleString()
}

/** A commit as the Git page draws one: its hash, its subject, who wrote it and when. */
function CommitValue({ commit }: { commit: Json }) {
  const author = text(commit.author)
  const when = text(commit.authoredAt)
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
      <ShortSha sha={text(commit.sha)} />
      <span className="min-w-0 font-medium">{text(commit.subject) ?? "No subject"}</span>
      {author && (
        <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
          <AuthorMark name={author} />
          {author}
          {when && <span>· {relativeTime(when)}</span>}
        </span>
      )}
    </span>
  )
}

/** A digest cut to what tells two apart, the whole of it on hover and a press away. */
function Digest({ value, label }: { value: string; label: string }) {
  const [algorithm, hash] = value.includes(":") ? value.split(":", 2) : ["", value]
  return (
    <span className="inline-flex min-w-0 items-center gap-1 font-mono text-xs" title={value}>
      {algorithm && <span className="text-muted-foreground/60">{algorithm}:</span>}
      <span className={tokenClass("id")}>{hash.slice(0, 12)}</span>
      <button
        type="button"
        aria-label={`Copy ${label.toLowerCase()}`}
        onClick={() => void copyText(value, `${label} copied`)}
        className="flex size-5 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:bg-accent hover:text-foreground"
      >
        <Copy className="size-3" />
      </button>
    </span>
  )
}

/**
 * A list of records, drawn by what its items carry: an image by its
 * reference, a check by its outcome, anything else as a record of its own.
 */
function ObjectList({ name, items, depth }: { name: string; items: Json[]; depth: number }) {
  if (items.length === 0) return null
  const images = items.every((item) => typeof item.reference === "string")
  const checks = items.every((item) => typeof item.outcome === "string" && "name" in item)
  return (
    <section aria-label={factLabel(name)} className="min-w-0 space-y-2">
      <GroupRule label={factLabel(name)} count={items.length} />
      {images ? (
        <ul className="divide-y divide-hairline">
          {items.map((item, index) => (
            <ImageRow key={index} image={item} />
          ))}
        </ul>
      ) : checks ? (
        <ul className="divide-y divide-hairline">
          {items.map((item, index) => (
            <CheckRow key={index} check={item} />
          ))}
        </ul>
      ) : (
        <div className="space-y-3">
          {items.map((item, index) => (
            <div key={index} className="min-w-0 rounded-lg border border-hairline p-3">
              <Block
                entries={Object.entries(item).filter(([, value]) => present(value))}
                depth={depth + 1}
              />
            </div>
          ))}
        </div>
      )}
    </section>
  )
}

/** An image as its product, then where it came from, what it is and how large. */
function ImageRow({ image }: { image: Json }) {
  const reference = String(image.reference)
  const metadata = isRecord(image.metadata) ? image.metadata : {}
  const platforms = (Array.isArray(image.platforms) ? image.platforms : metadata.platforms) as
    unknown[] | undefined
  const platform =
    text(platforms?.[0]) ??
    [text(image.os ?? metadata.os), text(image.architecture ?? metadata.architecture)]
      .filter(Boolean)
      .join("/")
  const digest = text(image.digest)
  return (
    <li className="flex min-w-0 items-center gap-3 py-2.5">
      <ProductLogo id={imageProduct(reference)} size="sm" />
      <div className="min-w-0 flex-1 space-y-1">
        <p className="flex min-w-0 items-center gap-2">
          <span className="truncate font-mono text-xs text-foreground">{reference}</span>
          {text(image.kind) && <Tag>{String(image.kind)}</Tag>}
        </p>
        <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
          {digest && <Digest value={digest} label="Digest" />}
          {platform && (
            <span className="inline-flex items-center gap-1">
              {platform.startsWith("linux") && <ProductGlyph id="linux" className="size-3" />}
              {platform}
            </span>
          )}
          {typeof image.sizeBytes === "number" && (
            <span className="numeric">{bytes(image.sizeBytes)}</span>
          )}
        </p>
      </div>
    </li>
  )
}

const CHECK_TONE: Record<string, "running" | "warning" | "danger" | "stopped"> = {
  passed: "running",
  warning: "warning",
  failed: "danger",
  unavailable: "warning",
  disabled: "stopped",
}

/** A health check as its outcome, its name, and its attempts' last answer. */
function CheckRow({ check }: { check: Json }) {
  const attempts = Array.isArray(check.attempts) ? (check.attempts as Json[]) : []
  const last = attempts.at(-1)
  const outcome = String(check.outcome)
  return (
    <li className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 py-2.5 text-body">
      <Status tone={CHECK_TONE[outcome] ?? "stopped"} label={factLabel(outcome)} />
      <span className="min-w-0 font-medium">{String(check.name)}</span>
      {text(check.kind) && <Tag>{String(check.kind)}</Tag>}
      {check.required === true && <Tag>required</Tag>}
      <span className="numeric ml-auto text-hint text-muted-foreground">
        {plural(attempts.length, "attempt")}
        {typeof last?.statusCode === "number" && ` · ${last.statusCode}`}
        {typeof last?.durationMillis === "number" &&
          ` in ${formatDuration(last.durationMillis / 1000)}`}
      </span>
    </li>
  )
}

/** Output kept as text, with its own name: a Dockerfile, a plan's notes. */
function TextBlock({ name, value }: { name: string; value: string }) {
  const dockerfile = /dockerfile/i.test(name)
  const lines = value.replace(/\n$/, "").split("\n")
  return (
    <CodeBlock
      title={dockerfile ? "Dockerfile" : factLabel(name)}
      mark={dockerfile ? <ProductGlyph id="docker" /> : undefined}
      copy={value}
      lines={lines.length}
    >
      {lines.map((line, index) => (
        <span key={index} className="flex">
          <span className="numeric w-8 shrink-0 pr-3 text-right text-muted-foreground/40 select-none">
            {index + 1}
          </span>
          <span className="min-w-0 wrap-anywhere whitespace-pre-wrap">
            {dockerfile ? <DockerfileLine line={line} /> : line}
          </span>
        </span>
      ))}
    </CodeBlock>
  )
}

/** A command the step ran, as a shell line: the program, its words, its flags. */
function CommandBlock({ argv }: { argv: string[] }) {
  const program = argv[0] ?? ""
  return (
    <CodeBlock
      title="Command"
      mark={hasProductLogo(program) ? <ProductGlyph id={program} /> : undefined}
      copy={argv.map(quote).join(" ")}
    >
      <span className="wrap-anywhere whitespace-pre-wrap">
        <span className="text-brand select-none">$ </span>
        {argv.map((word, index) => (
          <Fragment key={index}>
            {index > 0 && " "}
            <span
              className={
                index === 0
                  ? CODE.program
                  : word.startsWith("-")
                    ? CODE.flag
                    : PATH.test(word) || word === "."
                      ? CODE.path
                      : /[:/]/.test(word)
                        ? CODE.string
                        : undefined
              }
            >
              {quote(word)}
            </span>
          </Fragment>
        ))}
      </span>
    </CodeBlock>
  )
}

function quote(word: string) {
  return /[\s"'$`\\]/.test(word) ? `'${word.replaceAll("'", "'\\''")}'` : word
}

/**
 * The hues a code block draws in: the `--tag-*` rung the log console's tokens
 * sit on, so no kind outshouts another, and none of the status hues — a
 * string is not a success. Keys and keywords take the blues and the violet,
 * values the green and the pink, paths the cyan the console gives a path,
 * and the punctuation steps back.
 */
const CODE = {
  key: "text-[var(--tag-blue)]",
  keyword: "font-semibold text-[var(--tag-violet)]",
  literal: "text-[var(--tag-violet)]",
  string: "text-[var(--tag-green)]",
  number: "text-[var(--tag-pink)]",
  variable: "text-[var(--tag-pink)]",
  path: "text-[var(--tag-cyan)]",
  flag: "text-[var(--tag-cyan)]",
  digest: "text-[var(--tag-slate)]",
  comment: "text-muted-foreground/70 italic",
  punct: "text-muted-foreground/60",
  program: "font-semibold text-foreground",
}

const JSON_TOKEN =
  /("(?:[^"\\]|\\.)*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b|([{}[\],])/g

/** A record as JSON, laid out two spaces a level and coloured by what each token is. */
export function JsonCode({ value }: { value: unknown }) {
  const source = JSON.stringify(value, null, 2) ?? ""
  const out: React.ReactNode[] = []
  let at = 0
  for (const match of source.matchAll(JSON_TOKEN)) {
    const index = match.index ?? 0
    if (index > at) out.push(source.slice(at, index))
    const [whole, string, colon, number, literal] = match
    if (string !== undefined && colon !== undefined) {
      out.push(
        <span key={index} className={CODE.key}>
          {string}
        </span>,
        <span key={`${index}:`} className={CODE.punct}>
          {colon}
        </span>,
      )
    } else if (string !== undefined) {
      const inner = string.slice(1, -1)
      out.push(
        <span
          key={index}
          className={
            PATH.test(inner) || URL_TEXT.test(inner)
              ? CODE.path
              : DIGEST.test(inner) || HEX.test(inner)
                ? CODE.digest
                : CODE.string
          }
        >
          {string}
        </span>,
      )
    } else if (number !== undefined) {
      out.push(
        <span key={index} className={CODE.number}>
          {number}
        </span>,
      )
    } else if (literal !== undefined) {
      out.push(
        <span key={index} className={CODE.literal}>
          {literal}
        </span>,
      )
    } else {
      out.push(
        <span key={index} className={CODE.punct}>
          {whole}
        </span>,
      )
    }
    at = index + whole.length
  }
  if (at < source.length) out.push(source.slice(at))
  return <span className="whitespace-pre">{out}</span>
}

const DOCKER_WORD = /^(\s*)([A-Za-z]+)(\s|$)/
const DOCKER_PIECES = /(--[\w-]+(?:=\S*)?)|("(?:[^"\\]|\\.)*")|(\$\{?\w+\}?)|(\bAS\b)|(\S+)/g

/** A Dockerfile line: its instruction, its flags, its variables and its quoted words. */
function DockerfileLine({ line }: { line: string }) {
  if (/^\s*#/.test(line)) return <span className={CODE.comment}>{line}</span>
  const head = DOCKER_WORD.exec(line)
  if (!head) return <>{line}</>
  const from = head[2].toUpperCase() === "FROM"
  const rest = line.slice(head[0].length - head[3].length)
  const pieces: React.ReactNode[] = []
  let at = 0
  let first = true
  for (const match of rest.matchAll(DOCKER_PIECES)) {
    const index = match.index ?? 0
    if (index > at) pieces.push(rest.slice(at, index))
    const [whole, flag, quoted, variable, as] = match
    const className = flag
      ? CODE.flag
      : quoted
        ? CODE.string
        : variable
          ? CODE.variable
          : as
            ? CODE.keyword
            : from && first
              ? CODE.string
              : PATH.test(whole)
                ? CODE.path
                : undefined
    if (!flag) first = false
    pieces.push(
      <span key={index} className={className}>
        {whole}
      </span>,
    )
    at = index + whole.length
  }
  if (at < rest.length) pieces.push(rest.slice(at))
  return (
    <>
      {head[1]}
      <span className={CODE.keyword}>{head[2]}</span>
      {pieces}
    </>
  )
}

/**
 * Text kept as the engine kept it, in a recessed block with its name, its
 * length and a copy. A `Well`'s ground with a caption strip, so a Dockerfile
 * and the record around it read as one thing and its name.
 */
export function CodeBlock({
  title,
  mark,
  copy,
  lines,
  children,
}: {
  title: string
  mark?: React.ReactNode
  copy: string
  lines?: number
  children: React.ReactNode
}) {
  return (
    <figure className="min-w-0 overflow-hidden rounded-lg border border-hairline bg-surface-sunken">
      <figcaption className="flex min-h-8 min-w-0 items-center gap-2 border-b border-hairline px-3 py-1 text-hint text-muted-foreground">
        {mark}
        <span className="truncate font-medium text-foreground">{title}</span>
        {lines !== undefined && <span className="numeric">{plural(lines, "line")}</span>}
        <button
          type="button"
          aria-label={`Copy ${title.toLowerCase()}`}
          onClick={() => void copyText(copy, `${title} copied`)}
          className="ml-auto flex size-6 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:bg-accent hover:text-foreground"
        >
          <Copy className="size-3.5" />
        </button>
      </figcaption>
      <pre className="max-h-96 overflow-auto p-3 font-mono text-xs leading-relaxed">
        <code>{children}</code>
      </pre>
    </figure>
  )
}

import type { DeploymentConfiguration, DeploymentPreflightFinding } from "@/lib/types"
import {
  buildMethodProduct,
  frameworkProduct,
  hasProductLogo,
  imageProduct,
} from "@/components/product-logo"
import { frameworkLabel, humanize, sourceProduct } from "@/components/deploy/vocabulary"
import type { WizardErrors } from "@/components/deploy/deployment-defaults"
import type { ConfigureFlow, ConfigureStepKey } from "@/components/deploy/new-project/draft"
import type { PlanSection } from "@/components/deploy/new-project/plan-sections"

type Check = DeploymentConfiguration["checks"][number]
type Variable = DeploymentConfiguration["variables"][number]

/** What a check actually asks — a path, a command, or a port. */
export function checkTarget(check: Check) {
  const config = (check.config ?? {}) as {
    path?: string
    command?: string[]
    port?: number
    acceptAnyAnswer?: boolean
  }
  if (config.path) return `GET ${config.path}${config.acceptAnyAnswer ? " (any answer)" : ""}`
  if (check.kind === "docker_health") return "the image's HEALTHCHECK"
  if (config.command?.length) return config.command.join(" ")
  if (config.port) return `a connection on ${config.port}`
  return check.kind
}

/** How long the release will keep asking before it gives up. */
export function checkBudget(check: Check) {
  const config = (check.config ?? {}) as { attempts?: number; intervalSeconds?: number }
  if (!config.attempts) return undefined
  const seconds = config.attempts * (config.intervalSeconds ?? 1)
  return `up to ${seconds < 120 ? `${seconds}s` : `${Math.round(seconds / 60)} min`} to answer`
}

/** A generated secret's shape, in the words its framework's own generator would use. */
export function secretShape(variable: Variable) {
  switch (variable.generateFormat) {
    case "hex":
      return `${variable.generate} hex characters`
    case "base64":
      return `${variable.generate} random bytes, base64`
    case "laravel":
      return `a Laravel base64: key over ${variable.generate} random bytes`
    case "keylist":
      return `four keys of ${variable.generate} random bytes each`
  }
  return `${variable.generate} characters`
}

/**
 * "3 declared · 2 typed", dropping whichever is zero.
 *
 * Two different things reach the container and the screen counted only one of
 * them: a reviewed template declares its own variables and mints its own
 * secrets, so a plan carrying five read "1 value" — the rows somebody had
 * typed into the environment editor. A Git repository is the other way round
 * and declares none.
 */
export function variablesFact(declared: number, typed: number) {
  const parts = [declared > 0 && `${declared} declared`, typed > 0 && `${typed} typed`].filter(
    Boolean,
  )
  return parts.length === 0 ? "None" : parts.join(" · ")
}

/** What a row of the plan is drawn on when no product names it. */
export type PlanGlyph =
  | "source"
  | "build"
  | "container"
  | "address"
  | "checks"
  | "limits"
  | "storage"
  | "variables"
  | "preflight"

/**
 * How a row stands: nothing to say, worth a look before Deploy, wrong in a way
 * preflight or the form will refuse, or answered by this server.
 */
export type PlanTone = "default" | "warning" | "danger" | "passed"

export type PlanRow = {
  key: string
  /** The accessible name of the row's press: what it opens. */
  verb: string
  step: ConfigureStepKey
  /** The fields the row is a reading of, when they are a section of their step. */
  section?: PlanSection
  label: string
  /** The product it is drawn as, when one is known rather than guessed. */
  product?: string
  glyph: PlanGlyph
  /** What the row decides, in words. */
  reading: string
  /** What qualifies it, a step back: "HTTPS · +1 more". */
  facts?: string
  /** The same decision as the server will run it: a command, the paths it keeps, names. */
  code?: string
  /** How `code` is coloured; a command unless it says otherwise. */
  codeKind?: "shell" | "path" | "name"
  /** The branch and commit a source row builds. */
  ref?: { branch?: string; revision?: string }
  tone: PlanTone
}

export type PlanGroup = { step: ConfigureStepKey; label: string; rows: PlanRow[] }

const BUILD_ERRORS = [
  "buildMethod",
  "nodeVersion",
  "pythonVersion",
  "javaVersion",
  "dotnetVersion",
  "target",
]

/**
 * The plan a setup is building, as the rows of its rail: each of the four
 * steps holding the parts of the plan it decides, each part with the product
 * it is, what it currently says, and whether anything about it wants a look.
 *
 * It is the drawing that stood beside the form — source, build, runtime,
 * address — read down the way the run page reads a release, with the parts the
 * drawing could not hold (the health check, the limits, the storage, the
 * environment, the server's own check) given rows of their own rather than a
 * hint under somebody else's.
 */
export function planGroups({
  flow,
  branch,
  errors,
  variableCount,
  checking,
  findings,
  automatic,
}: {
  flow: ConfigureFlow
  branch?: string
  errors: WizardErrors
  /** Values that will reach the container from the environment editor. */
  variableCount: number
  checking: boolean
  /** Preflight's answer for the plan as it stands, when there is one. */
  findings?: DeploymentPreflightFinding[]
  /** Whether a push deploys, for a source that watches a branch. */
  automatic?: boolean
}): PlanGroup[] {
  const { source, configuration, candidate, profile } = flow
  const build = configuration.build
  const runtime = configuration.runtime
  const worker = profile === "worker"
  const compose = source.kind === "compose" || build.method === "compose"
  const builds = build.method !== "image" && build.method !== "none"
  const port = runtime.internalPort ?? 0
  const revision = flow.detection?.source?.revision ?? flow.detection?.source?.digest

  const sourceId = sourceProduct(
    {
      sourceKind: source.kind,
      sourceRef: source.ref ?? "",
      sourceRepository: source.image ?? source.repository ?? "",
      sourceRemote: source.url ?? "",
    },
    source,
  )
  const framework = candidate?.framework ?? build.framework
  // What runs, by the resolver the overview's Runtime node reads: a
  // repository's language, Docker for a Dockerfile and nginx for a static
  // site; the product an image or a template is; Compose for a stack.
  const runtimeId =
    source.kind === "image"
      ? imageProduct(source.image ?? "")
      : source.kind === "blueprint"
        ? source.blueprintId
        : compose
          ? "docker-compose"
          : buildMethodProduct(build.method, {
              recipe: build.recipe,
              packageManager: build.packageManager,
            })

  const sourceRow: PlanRow = {
    key: "source",
    verb: "Change the source",
    step: "project",
    section: "source",
    label: "Source",
    product: hasProductLogo(sourceId) ? sourceId : undefined,
    glyph: "source",
    reading: flow.sourceLabel,
    // A template is the version of it this dashboard reviewed, which says more
    // than the digest its image resolved to.
    ...(source.kind === "blueprint"
      ? { code: flow.detection?.source?.ref, codeKind: "name" as const }
      : { ref: branch || revision ? { branch, revision } : undefined }),
    tone: flow.detection?.unavailable ? "warning" : "default",
  }

  const buildRow: PlanRow = {
    key: "build",
    verb: "Change the build settings",
    step: "project",
    section: "build",
    label: "Build",
    product: builds
      ? (frameworkProduct(framework) ?? (hasProductLogo(runtimeId) ? runtimeId : undefined))
      : undefined,
    glyph: "build",
    reading: builds
      ? build.method === "recipe"
        ? `Automatic recipe${build.recipe ? ` · ${frameworkLabel(framework ?? build.recipe)}` : ""}`
        : humanize(build.method)
      : source.kind === "import"
        ? "Adopted as it runs"
        : source.kind === "blueprint"
          ? "No build · a reviewed template"
          : "No build · pulled at its digest",
    code: builds
      ? build.method === "dockerfile"
        ? `${build.dockerfile || "Dockerfile"}${build.target ? ` --target ${build.target}` : ""}`
        : build.buildCommand || undefined
      : undefined,
    tone: BUILD_ERRORS.some((key) => errors[key])
      ? "danger"
      : builds && (!candidate || (candidate.needsDecision?.length ?? 0) > 0)
        ? "warning"
        : "default",
  }

  const containerRow: PlanRow = {
    key: "container",
    verb: "Change the runtime settings",
    step: "runtime",
    section: "runtime",
    label: "Container",
    product: hasProductLogo(runtimeId) ? runtimeId : undefined,
    glyph: "container",
    reading: worker
      ? "No port · runs in the background"
      : port > 0
        ? `Port ${port} · ${runtime.strategy === "blue_green" ? "candidate first" : "stop first"}`
        : "No port set",
    code: builds && build.method !== "dockerfile" ? build.startCommand || undefined : undefined,
    tone: errors.internalPort ? "danger" : !worker && port === 0 ? "warning" : "default",
  }

  const domain = configuration.domains[0]
  const addressRow: PlanRow = {
    key: "address",
    verb: "Change the public address",
    step: "runtime",
    section: "address",
    label: "Address",
    // The authority the release asks for its certificate, as the run page
    // draws the step that asks it; a plain-HTTP name is only a name.
    product: domain?.https ? "lets-encrypt" : undefined,
    glyph: "address",
    reading: domain ? domain.hostname || "No hostname yet" : "No public address",
    facts: domain
      ? [
          domain.https ? "HTTPS" : "HTTP",
          configuration.domains.length > 1 && `+${configuration.domains.length - 1} more`,
          domain.protection && "password protected",
        ]
          .filter(Boolean)
          .join(" · ")
      : "Reach it through the port it publishes",
    tone: domain && !domain.hostname ? "warning" : "default",
  }

  const readiness = configuration.checks.find((check) => check.phase === "readiness")
  const gated = profile === "web" || profile === "static"
  const checksRow: PlanRow = {
    key: "checks",
    verb: "Change the health check",
    step: "runtime",
    section: "checks",
    label: "Health check",
    glyph: "checks",
    reading: readiness
      ? [readiness.name || "Readiness", checkBudget(readiness)].filter(Boolean).join(" · ")
      : "No readiness check",
    code: readiness ? checkTarget(readiness) : undefined,
    tone: !readiness && gated ? "warning" : "default",
  }

  const limits = [
    runtime.memoryMb ? `${runtime.memoryMb} MB` : undefined,
    runtime.cpus ? `${runtime.cpus} CPU` : undefined,
  ].filter(Boolean)
  const limitsRow: PlanRow = {
    key: "limits",
    verb: "Change the resource limits",
    step: "runtime",
    section: "limits",
    label: "Resources",
    glyph: "limits",
    reading: compose
      ? limits.length > 0
        ? `${limits.join(" · ")} override · service limits in Compose source`
        : "Service limits are in Compose source"
      : limits.length > 0
        ? limits.join(" · ")
        : // Said here rather than left to be discovered inside a fold: on one
          // server an unbounded container is the thing that takes the
          // dashboard down with it.
          "No memory or CPU limit",
    tone:
      errors.maxRequestBodyMb || errors.resource
        ? "danger"
        : !compose && limits.length === 0
          ? "warning"
          : "default",
  }

  const mounts = runtime.mounts ?? []
  const storageRow: PlanRow = {
    key: "storage",
    verb: "Change the storage",
    step: "runtime",
    section: "storage",
    label: "Storage",
    glyph: "storage",
    reading:
      mounts.length > 0
        ? `${mounts.length} ${mounts.length === 1 ? "mount" : "mounts"} kept between releases`
        : compose
          ? "Kept in Compose source"
          : "Nothing survives a rebuild",
    code: mounts.length > 0 ? mounts.map((mount) => mount.target).join("  ") : undefined,
    codeKind: "path",
    tone: "default",
  }

  const declared = configuration.variables.length
  const unanswered = configuration.variables.filter(
    (variable) => variable.required && !variable.reference && !variable.value && !variable.generate,
  )
  const variablesRow: PlanRow = {
    key: "variables",
    verb: "Change the variables",
    step: "variables",
    label: "Environment",
    glyph: "variables",
    reading:
      unanswered.length > 0
        ? `${unanswered.length} ${unanswered.length === 1 ? "value" : "values"} still needed`
        : variablesFact(declared, variableCount) === "None"
          ? "No variables"
          : variablesFact(declared, variableCount),
    code:
      unanswered.length > 0 ? unanswered.map((variable) => variable.name).join("  ") : undefined,
    codeKind: "name",
    tone: unanswered.length > 0 ? "warning" : "default",
  }

  const passed = findings?.filter((finding) => finding.severity === "pass").length ?? 0
  const blocked =
    findings?.filter((finding) => finding.severity === "blocked" || finding.severity === "decision")
      .length ?? 0
  const warned = findings?.filter((finding) => finding.severity === "warning").length ?? 0
  const preflightRow: PlanRow = {
    key: "preflight",
    verb: "Review the checks",
    step: "review",
    label: "Checked against this server",
    glyph: "preflight",
    reading: checking
      ? "Asking this server about the plan…"
      : !findings
        ? "Asked when you reach Review"
        : blocked > 0
          ? `${blocked} to fix before it deploys`
          : warned > 0
            ? `${warned} to acknowledge · ${passed} passed`
            : `${passed} ${passed === 1 ? "check" : "checks"} passed`,
    tone:
      !findings || checking
        ? "default"
        : blocked > 0
          ? "danger"
          : warned > 0
            ? "warning"
            : "passed",
  }

  const review: PlanRow[] = [preflightRow]
  if (automatic !== undefined)
    review.push({
      key: "automatic",
      verb: "Change automatic deployment",
      step: "review",
      section: "automatic",
      label: "Automatic deployment",
      product: hasProductLogo(sourceId) ? sourceId : undefined,
      glyph: "source",
      reading: automatic ? "Each new commit deploys itself" : "Only when you deploy",
      ref: automatic && branch ? { branch } : undefined,
      tone: "default",
    })

  return [
    { step: "project", label: "Project", rows: [sourceRow, buildRow] },
    {
      step: "runtime",
      label: "Runtime",
      // A worker answers no requests, so it has no address to draw.
      rows: worker
        ? [containerRow, checksRow, limitsRow, storageRow]
        : [containerRow, addressRow, checksRow, limitsRow, storageRow],
    },
    { step: "variables", label: "Variables", rows: [variablesRow] },
    { step: "review", label: "Review", rows: review },
  ]
}

import type {
  DeploymentBuildMethod,
  DeploymentConfiguration,
  DeploymentDetection,
  DeploymentDetectedJavaBuild,
  DeploymentDetectedReadiness,
  DeploymentDetectionCandidate,
  DeploymentDraftSource,
  DeploymentSourceMode,
  NodePackageManager,
  WorkloadProfile,
} from "@/lib/types"
import type { EnvironmentRow } from "@/components/deploy/new-project/draft"

export const PYTHON_VERSION = /^3\.(10|11|12|13|14)$/
/** A Debian package name, as the Python recipe's system packages are validated. */
export const SYSTEM_PACKAGE = /^[a-z0-9][a-z0-9.+-]+$/
export const MAX_SYSTEM_PACKAGES = 32

/**
 * The system packages a plan starts with: another platform's (an Aptfile's),
 * which the recipe installs only while the plan names them. The packages the
 * dependencies need are installed by the recipe itself and are not repeated.
 */
export function detectedSystemPackages(candidate?: DeploymentDetectionCandidate) {
  if (candidate?.recipe !== "python") return undefined
  const names = (candidate.systemPackages ?? [])
    .filter((pkg) => !pkg.automatic && SYSTEM_PACKAGE.test(pkg.name))
    .map((pkg) => pkg.name)
  return names.length ? names.slice(0, MAX_SYSTEM_PACKAGES) : undefined
}

/**
 * The Go releases the recipe builds with, oldest first: `goRecipeFamilies` in
 * the backend's build_go.go, which a Go test keeps equal to this list. The
 * oldest is past upstream support, so it builds only when pinned.
 */
export const GO_VERSIONS = ["1.25", "1.26", "1.27"]
export const GO_VERSION = new RegExp(
  `^1\\.(${GO_VERSIONS.map((family) => family.slice(2)).join("|")})(\\.[0-9]{1,3})?$`,
)

/** The JDK releases the Java recipe builds and runs on, as `planning_compiled.go` accepts them. */
export const JAVA_VERSIONS = ["8", "11", "17", "21", "25"]
export const JAVA_VERSION = /^(8|11|17|21|25)$/
/** The .NET releases the .NET recipe publishes for. */
export const DOTNET_VERSIONS = ["8.0", "9.0", "10.0"]
export const DOTNET_VERSION = /^(8|9|10)\.0$/

/** The request-body ceiling a route gets when the plan names none, and the most it may name. */
export const DEFAULT_MAX_REQUEST_BODY_MB = 64
export const MAX_REQUEST_BODY_MB = 10240
/**
 * What zero means, which depends on the proxy: host nginx routes are given
 * the 64 MB default because nginx's own refused a phone photo, while Caddy,
 * which has no default limit, is left without one.
 */
export const DEFAULT_REQUEST_BODY_LIMIT = `${DEFAULT_MAX_REQUEST_BODY_MB} MB on nginx, no limit on Caddy`

/**
 * The variables an application issues to itself — a session or signing
 * secret, an encryption key — as opposed to one a third party hands out. A
 * value for these is any long random string, so the form can mint one; an
 * API key from a provider it cannot.
 */
export const SELF_ISSUED_SECRET =
  /(^|_)(APP_KEY|APP_KEYS|APP_SECRET|SECRET_KEY|SECRET_KEY_BASE|SESSION_SECRET|JWT_SECRET|AUTH_SECRET|NEXTAUTH_SECRET|BETTER_AUTH_SECRET|PAYLOAD_SECRET|ENCRYPTION_KEY|COOKIE_SECRET|CSRF_SECRET|TOKEN_SECRET|SIGNING_SECRET|SIGNING_KEY|HASH_SALT|TOKEN_SALT)$/

/**
 * Names a provider issues even though they end like a self-issued secret:
 * STRIPE_SECRET_KEY, CLERK_SECRET_KEY, SUPABASE_JWT_SECRET, Auth.js's
 * AUTH_GITHUB_SECRET, any OAuth client or webhook secret. A random string is
 * never the right value for one, so the form never offers to make it.
 */
const PROVIDER_ISSUED =
  /^(STRIPE|CLERK|SUPABASE|GITHUB|GITLAB|GOOGLE|AUTH0|AWS|AZURE|OPENAI|ANTHROPIC|TWILIO|SENDGRID|RESEND|PAYPAL|SLACK|DISCORD|FIREBASE|CLOUDINARY|MAILGUN|POSTMARK|ALGOLIA|SENTRY|PUSHER|SHOPIFY|NOTION|LINEAR|OKTA|KEYCLOAK|LEMONSQUEEZY|PADDLE|PLAID)_|^AUTH_[A-Z0-9]+_(ID|SECRET)$|_CLIENT_SECRET$|_WEBHOOK_SECRET$/

export function canGenerateSecret(name: string) {
  return SELF_ISSUED_SECRET.test(name) && !PROVIDER_ISSUED.test(name)
}

/**
 * A fresh secret in the shape the variable's own framework expects: Laravel
 * reads a base64 32-byte key with its `base64:` prefix, Rails wants a long
 * hex string, Strapi's APP_KEYS is a list of four, and everything else takes
 * 32 random bytes as hex. Detected secrets are minted by the server when the
 * project is created; this is the Generate button's, for a row typed by hand.
 */
export type RandomBytes = (length: number) => Uint8Array<ArrayBuffer>

export function generateSecretValue(name: string, random: RandomBytes = randomBytes) {
  if (name === "APP_KEY") return `base64:${toBase64(random(32))}`
  if (name === "APP_KEYS") return [0, 1, 2, 3].map(() => toBase64(random(16))).join(",")
  if (name.endsWith("SECRET_KEY_BASE")) return toHex(random(64))
  return toHex(random(32))
}

function randomBytes(length: number): Uint8Array<ArrayBuffer> {
  const bytes = new Uint8Array(length)
  globalThis.crypto.getRandomValues(bytes)
  return bytes
}

function toHex(bytes: Uint8Array<ArrayBuffer>) {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("")
}

function toBase64(bytes: Uint8Array<ArrayBuffer>) {
  return btoa(String.fromCharCode(...bytes))
}

/**
 * The defaults and validation both deployment flows start from.
 *
 * Lifted out of the wizard when quick deploy arrived: the two screens create
 * the same kind of plan, and a default that existed in one of them was a plan
 * the other could not produce. Preflight refuses the same things either way,
 * so the answers to it have to live in one place.
 */

export type WizardErrors = Record<string, string>

export function sourceForProfile(profile: WorkloadProfile): DeploymentDraftSource {
  if (profile === "image") return sourceForMode("image_reference", profile)
  if (profile === "compose") return sourceForMode("compose_paste", profile)
  if (profile === "service") return sourceForMode("blueprint", profile)
  if (profile === "game") return sourceForMode("blueprint", profile)
  if (profile === "imported") return sourceForMode("existing_container", profile)
  return sourceForMode("git_url", profile)
}

export function sourceForMode(
  mode: DeploymentSourceMode,
  profile: WorkloadProfile,
): DeploymentDraftSource {
  if (mode === "image_reference")
    return {
      kind: "image",
      mode,
      image: profile === "game" ? "itzg/minecraft-server:java21" : "",
      credentialId: undefined,
    }
  if (mode === "compose_paste" || mode === "compose_upload")
    return { kind: "compose", mode, composeFiles: [{ path: "compose.yml", content: "", order: 0 }] }
  if (mode === "compose_git")
    return {
      kind: "compose",
      mode,
      url: "",
      ref: "main",
      composeFiles: [{ path: "compose.yml", content: "", order: 0 }],
    }
  if (mode === "compose_local")
    return {
      kind: "compose",
      mode,
      localPath: "",
      composeFiles: [{ path: "compose.yml", content: "", order: 0 }],
    }
  if (mode === "connected_repository")
    return { kind: "git", mode, provider: "github", repository: "", ref: "main" }
  if (mode === "local_checkout") return { kind: "git", mode, localPath: "" }
  if (mode === "git_url") return { kind: "git", mode, url: "", ref: "main" }
  if (mode === "blueprint")
    return {
      kind: "blueprint",
      mode,
      blueprintId: profile === "service" ? "uptime-kuma" : "minecraft-java",
      blueprintVersion: "1.0.0",
      blueprintInputs: {},
    }
  return {
    kind: "import",
    mode,
    resourceId: "",
    ...(mode === "existing_checkout" ? { localPath: "" } : {}),
  }
}

export function sourceModes(profile: WorkloadProfile): [DeploymentSourceMode, string][] {
  if (profile === "game")
    return [
      ["blueprint", "Reviewed game blueprint"],
      ["image_reference", "Docker registry image"],
    ]
  if (profile === "image") return [["image_reference", "Docker registry image"]]
  if (profile === "compose")
    return [
      ["compose_paste", "Paste Compose"],
      ["compose_upload", "Upload Compose files"],
      ["compose_git", "Compose files in Git"],
      ["compose_local", "Compose files on this server"],
    ]
  if (profile === "service")
    return [
      ["blueprint", "Reviewed blueprint"],
      ["image_reference", "Docker registry image"],
      ["compose_paste", "Paste Compose"],
    ]
  if (profile === "imported")
    return [
      ["existing_container", "Existing container"],
      ["existing_stack", "Existing Compose stack"],
      ["existing_checkout", "Existing Git checkout"],
    ]
  return [
    ["git_url", "Public or credentialed Git URL"],
    ["connected_repository", "Connected repository"],
    ["local_checkout", "Local checkout"],
  ]
}

export function validateSource(source: DeploymentDraftSource) {
  const errors: WizardErrors = {}
  if ((source.mode === "git_url" || source.mode === "compose_git") && !source.url?.trim())
    errors.url = "Enter the Git repository URL."
  if (source.mode === "connected_repository" && !source.repository?.trim())
    errors.repository = "Enter the provider repository as owner/name."
  if (
    ["local_checkout", "compose_local", "existing_checkout"].includes(source.mode) &&
    !source.localPath?.startsWith("/")
  )
    errors.localPath = "Enter an absolute path inside a deployment root."
  if (source.mode === "image_reference" && !source.image?.trim())
    errors.image = "Enter a Docker image reference."
  if (
    ["compose_paste", "compose_upload"].includes(source.mode) &&
    (!source.composeFiles?.length ||
      source.composeFiles.some((file) => !file.path || !file.content))
  )
    errors.compose = "Every Compose file needs a relative .yml/.yaml path and content."
  if (["compose_git", "compose_local"].includes(source.mode)) {
    const paths = source.composeFiles?.map((file) => file.path) ?? []
    if (
      paths.length > 16 ||
      paths.some(
        (path) =>
          !path ||
          path.startsWith("/") ||
          path.split("/").includes("..") ||
          (!path.endsWith(".yml") && !path.endsWith(".yaml")),
      ) ||
      new Set(paths).size !== paths.length
    )
      errors.compose = "Use at most 16 unique relative .yml/.yaml paths."
  }
  if (["existing_container", "existing_stack"].includes(source.mode) && !source.resourceId?.trim())
    errors.resource = "Name the existing resource to inspect."
  if (source.mode === "blueprint" && !source.blueprintId) errors.source = "Choose a blueprint."
  return errors
}

// blueprintAcceptances names the acceptance inputs the operator ticked. It is
// the one place the wizard reads consent, so the source step and the plan can
// never disagree about whether a licence was accepted.
export function blueprintAcceptances(source?: DeploymentDraftSource): string[] {
  if (!source || source.mode !== "blueprint") return []
  return Object.entries(source.blueprintInputs ?? {})
    .filter(([, value]) => value === "true")
    .map(([name]) => name)
    .filter((name) => name === "eula")
}

/**
 * Detected commands run a script through the manager the lockfile implied, so
 * choosing a manager has to move that runner with it: oven/bun has no npm and
 * the node image has no bun, and a stale runner fails after a clean install.
 * Only the plain `<manager> run <script>` form is rewritten; anything else is
 * the operator's own command.
 */
const EXEC_RUNNER: Record<NodePackageManager, string> = {
  npm: "npx",
  pnpm: "pnpm exec",
  yarn: "yarn",
  bun: "bunx",
}

/**
 * Moves a detected command to another package manager: each `&&` segment
 * that is a plain `<manager> run <script>`, npm's, pnpm's or Yarn's `start`
 * and `test` shorthands, or a dependency binary run through the manager
 * (`npx prisma migrate deploy`) follows the choice, after any `NAME=value`
 * assignments in front of it; anything else is the operator's own command and
 * is left alone. A bare `yarn <thing>` is only a binary run when it is
 * chained, because on its own it is just as likely a script shorthand. The
 * backend moves a saved command the same way at build time (nodeRunnerFor in
 * build_node_install.go); the two are held to the same rows.
 */
export function withPackageManagerRunner(command: string, manager?: NodePackageManager) {
  if (!manager) return command
  const segments = command.trim().split(/\s+&&\s+/)
  return segments
    .map((segment) => {
      const [, assignments, body] = /^((?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+)*)(.*)$/.exec(segment)!
      const script = /^(?:bun|npm|pnpm|yarn) run (\S+)$/.exec(body)
      if (script) return `${assignments}${manager} run ${script[1]}`
      const shorthand = /^(npm|pnpm|yarn) (start|test)$/.exec(body)
      if (shorthand && shorthand[1] !== manager)
        return `${assignments}${manager} run ${shorthand[2]}`
      const binary = /^(?:npx|bunx|pnpm exec) (.+)$/.exec(body)
      if (binary) return `${assignments}${EXEC_RUNNER[manager]} ${binary[1]}`
      const yarnBinary = segments.length > 1 ? /^yarn (?!run )(.+)$/.exec(body) : null
      if (yarnBinary) return `${assignments}${EXEC_RUNNER[manager]} ${yarnBinary[1]}`
      return segment
    })
    .join(" && ")
}

/**
 * The release strategy a profile and its storage allow. Candidate-first needs
 * two containers side by side, which preflight refuses for anything but a web
 * or static workload — and for any plan with a writable mount, because two
 * releases would write the same data at once.
 */
export function releaseStrategy(
  profile: WorkloadProfile,
  mounts: DeploymentConfiguration["runtime"]["mounts"] = [],
): DeploymentConfiguration["runtime"]["strategy"] {
  const gated = profile === "web" || profile === "static"
  return gated && !mounts.some((mount) => !mount.readOnly) ? "blue_green" : "stop_first"
}

/**
 * A managed volume's name for this project: the name as a slug, a hash, and
 * what the volume holds — the `<slug>-<hash>-<purpose>` shape a template's
 * volumes already have. The hash covers the exact name and the draft the
 * plan belongs to: two names that slug alike, and one repository imported
 * twice under the name it arrives with, never share data. Preflight refuses a
 * volume another project already manages all the same.
 */
export function projectVolumeName(projectName: string, target: string, draftId = "") {
  const slug =
    projectName
      .toLowerCase()
      .replace(/[\s_]+/g, "-")
      .replace(/[^a-z0-9-]/g, "")
      .replace(/-+/g, "-")
      .replace(/^-|-$/g, "")
      .slice(0, 40)
      .replace(/-$/, "") || "app"
  const purpose =
    (target.split("/").filter(Boolean).pop() ?? "")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "") || "data"
  return `${slug}-${fnv1a(draftId ? `${projectName}\u0000${draftId}` : projectName)}-${purpose}`
}

function fnv1a(value: string) {
  let hash = 0x811c9dc5
  for (const byte of new TextEncoder().encode(value)) {
    hash = Math.imul(hash ^ byte, 0x01000193) >>> 0
  }
  return hash.toString(16).padStart(8, "0")
}

/**
 * The volumes the state detection found needs: one managed volume per
 * directory it can stand on, with a storage dependency that says what it
 * holds, so Review names it and lifecycle knows the project owns it. State
 * with no such directory gets no volume here; preflight names it instead.
 */
export function persistentStorage(
  candidate: DeploymentDetectionCandidate | undefined,
  projectName: string,
  draftId = "",
): Pick<DeploymentConfiguration["runtime"], "mounts"> &
  Pick<DeploymentConfiguration, "dependencies"> {
  const reasons = new Map<string, string[]>()
  for (const entry of candidate?.persistentPaths ?? []) {
    if (!entry.target) continue
    reasons.set(entry.target, [...(reasons.get(entry.target) ?? []), entry.reason])
  }
  const mounts: NonNullable<DeploymentConfiguration["runtime"]["mounts"]> = []
  const dependencies: DeploymentConfiguration["dependencies"] = []
  const names = new Set<string>()
  for (const [target, why] of reasons) {
    let source = projectVolumeName(projectName, target, draftId)
    for (let index = 2; names.has(source); index++)
      source = `${projectVolumeName(projectName, target, draftId)}-${index}`
    names.add(source)
    mounts.push({ source, target, ownership: "managed" })
    dependencies.push({
      kind: "storage",
      ownership: "managed",
      resourceKind: "docker_volume",
      resourceId: source,
      config: { purpose: [...new Set(why)].join("; "), data: true },
    })
  }
  return { mounts, dependencies }
}

/**
 * The plan's declarations for the variables that move detected state onto
 * its volume. The value is a path, not a secret, so it travels in the plan;
 * and it reaches the runtime and release tasks — the build has no volume
 * mounted, and a build that opens the database there would fail where the
 * committed default still works. A variable detection saw the build itself
 * read (Prisma 7's `prisma.config`, which `prisma generate` loads) reaches the
 * build too, since there an unset one fails before any file is opened. A
 * declaration the plan already makes for the same name is replaced, unless it
 * is a reference or a generated secret.
 */
export function withPersistentVariables(
  variables: DeploymentConfiguration["variables"],
  candidate: DeploymentDetectionCandidate | undefined,
): DeploymentConfiguration["variables"] {
  const moved = new Map<string, string>()
  for (const entry of candidate?.persistentPaths ?? []) {
    if (entry.target && entry.variable && entry.value && !moved.has(entry.variable))
      moved.set(entry.variable, entry.value)
  }
  const kept = variables.filter(
    (variable) => !moved.has(variable.name) || variable.reference || variable.generate,
  )
  const declared = new Set(kept.map((variable) => variable.name))
  const readByBuild = new Set(
    (candidate?.variables ?? [])
      .filter((variable) => variable.phase === "build")
      .map((variable) => variable.name),
  )
  return [
    ...kept,
    ...[...moved]
      .filter(([name]) => !declared.has(name))
      .map(([name, value]) => ({
        name,
        sensitivity: "plain" as const,
        scopes: readByBuild.has(name)
          ? ["runtime", "release_task", "build"]
          : ["runtime", "release_task"],
        value,
      })),
  ]
}

/**
 * Moves the plan's commands to the package manager chosen, where `undefined`
 * is "from the lockfile" — the manager detection resolved, not the command's
 * old runner. A command that is still one detection proposed is swapped whole
 * for the one detection proposes for the new manager, which carries what a
 * pattern cannot (SvelteKit's `bun ./build/index.js` under Bun is `node build`
 * under npm); a command the operator wrote keeps its words, with only its
 * plain runner segments moved.
 */
export function commandsForPackageManager(
  candidate: DeploymentDetectionCandidate | undefined,
  commands: { buildCommand?: string; startCommand?: string },
  manager: NodePackageManager | undefined,
) {
  const target = manager ?? candidate?.packageManager
  if (!target) return commands
  const installs = candidate?.nodeInstalls ?? []
  const chosen = installs.find((install) => install.manager === target)
  const move = (current: string | undefined, key: "buildCommand" | "startCommand") => {
    if (!current) return current
    const detected = installs.some((install) => install[key] === current)
    if (detected && chosen?.[key]) return chosen[key]
    return withPackageManagerRunner(current, target)
  }
  return {
    buildCommand: move(commands.buildCommand, "buildCommand"),
    startCommand: move(commands.startCommand, "startCommand"),
  }
}

export const PACKAGE_MANAGER_LABELS: Record<NodePackageManager, string> = {
  bun: "Bun",
  npm: "npm",
  pnpm: "pnpm",
  yarn: "Yarn",
}

export type PackageManagerOption = {
  value: NodePackageManager
  label: string
  /** What detection read for this choice: whether its lockfile matches package.json. */
  hint: string
  /** The build would refuse it — another manager's lockfile is committed and this one has none. */
  disabled: boolean
}

/**
 * Each package manager as an option, with what choosing it means for this
 * repository: the incident this exists for was an operator choosing npm
 * between two lockfiles with nothing saying that package-lock.json was fifteen
 * dependencies behind while bun.lock matched.
 */
export function packageManagerOptions(
  candidate: DeploymentDetectionCandidate | undefined,
): PackageManagerOption[] {
  return (["bun", "npm", "pnpm", "yarn"] as const).map((manager) => {
    const install = candidate?.nodeInstalls?.find((item) => item.manager === manager)
    const blocked = install?.findings?.find((finding) => finding.severity === "blocked")
    let hint = ""
    if (!install) {
      // A candidate detected before installs were recorded names lockfiles only.
      hint = candidate?.packageManagers?.includes(manager) ? "lockfile committed" : ""
    } else if (blocked) {
      hint = blocked.code === "package_manager_lockfile_missing" ? "no lockfile" : blocked.title
    } else if (!install.lockfile) {
      hint = "no lockfile · unpinned install"
    } else {
      const lockfile = candidate?.lockfiles?.find((item) => item.path === install.lockfile)
      hint =
        lockfile?.state === "in_sync"
          ? `${install.lockfile} matches`
          : lockfile?.state === "stale"
            ? `${install.lockfile} out of sync`
            : install.lockfile
    }
    return {
      value: manager,
      label: PACKAGE_MANAGER_LABELS[manager],
      hint,
      disabled: Boolean(blocked),
    }
  })
}

/** What "from the lockfile" resolves to, for the option's own hint. */
export function automaticPackageManagerHint(candidate: DeploymentDetectionCandidate | undefined) {
  if (candidate?.packageManager) return PACKAGE_MANAGER_LABELS[candidate.packageManager]
  return (candidate?.packageManagers?.length ?? 0) > 1 ? "choose one" : ""
}

/**
 * The reading under the field for the manager a plan resolves to: what its
 * lockfile says and the install the recipe runs, e.g. "bun.lock matches
 * package.json · bun install --frozen-lockfile".
 */
export function packageManagerReading(
  candidate: DeploymentDetectionCandidate | undefined,
  manager: NodePackageManager | undefined,
) {
  const target = manager ?? candidate?.packageManager
  const install = candidate?.nodeInstalls?.find((item) => item.manager === target)
  if (!install?.install) return undefined
  const lockfile = candidate?.lockfiles?.find((item) => item.path === install.lockfile)
  return [
    lockfile?.note ?? (install.lockfile ? undefined : "No lockfile is committed"),
    install.install,
  ]
    .filter(Boolean)
    .join(" · ")
}

export function defaultConfiguration(
  profile: WorkloadProfile,
  candidate?: DeploymentDetectionCandidate,
  source?: DeploymentDraftSource,
  detection?: DeploymentDetection,
  projectName?: string,
  draftId?: string,
): DeploymentConfiguration {
  const game = profile === "game"
  const image = source?.image ?? (game ? "itzg/minecraft-server:java21" : "")
  const method: DeploymentBuildMethod =
    candidate?.buildMethod ??
    (source?.kind === "image" ? "image" : source?.kind === "compose" ? "compose" : "none")
  const packagedStatic =
    method === "static" || (method === "recipe" && !!candidate?.outputDirectory)
  // A port the source did not name is an unanswered question, not a 3000 to
  // assume: `Port` is `omitempty`, so an undetected one arrives as undefined,
  // and every recipe framework already supplies its own default. Inventing
  // one here let a Dockerfile with no EXPOSE reach Review with a readiness
  // check built on a port nothing listens to.
  const port = packagedStatic ? 80 : (candidate?.port ?? (game ? 25565 : 0))
  const composeVariables = detection?.compose?.variables ?? []
  const name = projectName || candidate?.name || "app"
  const storage = persistentStorage(candidate, name, draftId)
  // A world is one project's: a fixed name would hand the second server the
  // first one's world, and preflight refuses a volume another project owns.
  const mounts = game
    ? [
        {
          source: projectVolumeName(name, "/data", draftId),
          target: "/data",
          ownership: "managed" as const,
        },
      ]
    : storage.mounts
  return {
    build: {
      method,
      recipe: method === "recipe" ? candidate?.recipe : undefined,
      rootDirectory: candidate?.root,
      buildCommand: candidate?.buildCommand,
      startCommand: candidate?.startCommand,
      outputDirectory: candidate?.outputDirectory,
      dockerfile: method === "dockerfile" ? (candidate?.dockerfile ?? "Dockerfile") : undefined,
      target: method === "dockerfile" ? candidate?.dockerfileTarget : undefined,
      pythonVersion: candidate?.recipe === "python" ? candidate.pythonVersion : undefined,
      systemPackages: detectedSystemPackages(candidate),
      goPackage: candidate?.recipe === "go" ? candidate.goPackage : undefined,
      spaFallback: packagedStatic && candidate?.spaFallback ? true : undefined,
      noCache: false,
      secrets: [],
      releaseTasks: detectedReleaseTasks(method, candidate),
    },
    runtime: {
      image,
      command: [],
      internalPort: port,
      hostPort: game ? 25565 : 0,
      bindAddress: "127.0.0.1",
      strategy: releaseStrategy(profile, mounts),
      privileged: false,
      hostNetwork: false,
      capabilities: [],
      devices: [],
      mounts,
    },
    variables: withPersistentVariables(
      [
        ...composeVariables.map((name) => ({
          name,
          // A browser-public value is compiled into the page by design, and
          // only a plain one can reach a Compose build argument.
          sensitivity: BROWSER_PREFIX.test(name) ? ("plain" as const) : ("secret" as const),
          scopes: ["runtime"],
          required: true,
          reference: "",
        })),
        // A blueprint that was accepted in the source step carries that consent
        // into the plan. Asking for the same agreement twice is not twice as
        // careful; it is one acceptance the operator can disagree with itself.
        ...blueprintAcceptances(source).map((name) => ({
          name: "EULA",
          sensitivity: "plain" as const,
          scopes: ["runtime"],
          required: true,
          reference: `\${{blueprint.${source?.blueprintId}-${name}-accepted}}`,
        })),
        ...detectedVariableDeclarations(candidate, profile),
      ],
      candidate,
    ),
    dependencies: storage.dependencies,
    checks: defaultChecks(profile, port, candidate?.readiness),
    domains: [],
  }
}

/**
 * The variables the deployment's place behind the proxy decides — Auth.js's
 * AUTH_TRUST_HOST, NEXTAUTH_URL following the primary domain, HOST for a
 * server that binds it — as plain plan variables: visible in the plan, and
 * removable like any other, rather than baked into the image.
 */
export function networkVariables(
  candidate?: DeploymentDetectionCandidate,
): DeploymentConfiguration["variables"] {
  return (candidate?.networkVariables ?? []).map((variable) => ({
    name: variable.name,
    sensitivity: "plain" as const,
    scopes: ["runtime"],
    ...(variable.domainTemplate
      ? { value: "", domainTemplate: variable.domainTemplate }
      : { value: variable.value }),
  }))
}

/**
 * The command a repository declares runs once before each release — a
 * Procfile `release:`, fly.toml's release_command, render.yaml's
 * preDeployCommand, a Phoenix release's bin/migrate — planned as a task in
 * the release's own image, where the application's toolchain is. Left out of
 * a plan that builds no image to run it in.
 */
export function detectedReleaseTasks(
  method: DeploymentBuildMethod,
  candidate?: DeploymentDetectionCandidate,
): NonNullable<DeploymentConfiguration["build"]["releaseTasks"]> {
  const command = candidate?.releaseCommand?.trim()
  if (!command || method === "none") return []
  return [{ name: "release", command, timeoutSeconds: 600, env: [], runner: "image" }]
}

/**
 * Why a detected candidate cannot build as detected, in the words detection
 * used, for the list the operator chooses from.
 */
export function candidateBlocker(candidate: DeploymentDetectionCandidate): string | undefined {
  const blocked = candidate.imageBuildIssues?.find((issue) => issue.severity === "blocked")
  if (blocked) return blocked.detail
  if (candidate.recipeIssue) return candidate.recipeIssue
  if (
    candidate.recipe === "node" &&
    !candidate.packageManager &&
    (candidate.packageManagers?.length ?? 0) > 1
  )
    return `choose the package manager: ${candidate.packageManagers!.join(", ")}`
  if (candidate.buildMethod === "compose")
    return "deploy the repository as a Compose source to analyse and build its services"
  return undefined
}

/**
 * A Compose file found in a repository is only named by detection; its
 * services are analysed, and can build, when the same repository is read as
 * a Compose source. The switch keeps the repository, branch, credential and
 * subdirectory, and selects the files the candidate was found from.
 */
export function composeSourceForCandidate(
  source: DeploymentDraftSource,
  candidate?: DeploymentDetectionCandidate,
): DeploymentDraftSource | undefined {
  if (candidate?.buildMethod !== "compose") return undefined
  const composeFiles = candidate.evidence
    .filter((item) => item.reason === "Compose configuration")
    .map((item, order) => ({ path: item.path, content: "", order }))
  if (composeFiles.length === 0) return undefined
  if (source.mode === "git_url")
    return {
      kind: "compose",
      mode: "compose_git",
      url: source.url,
      ref: source.ref,
      credentialId: source.credentialId,
      subdirectory: source.subdirectory,
      includeSubmodules: source.includeSubmodules,
      includeLfs: source.includeLfs,
      composeFiles,
    }
  if (source.mode === "local_checkout")
    return {
      kind: "compose",
      mode: "compose_local",
      localPath: source.localPath,
      subdirectory: source.subdirectory,
      composeFiles,
    }
  const url = connectedRepositoryRemote(source)
  if (source.mode === "connected_repository" && url)
    return {
      kind: "compose",
      mode: "compose_git",
      url,
      ref: source.ref,
      credentialId: source.credentialId,
      subdirectory: source.subdirectory,
      includeSubmodules: source.includeSubmodules,
      includeLfs: source.includeLfs,
      composeFiles,
    }
  return undefined
}

const PROVIDER_ORIGINS: Record<string, string> = {
  github: "https://github.com",
  gitlab: "https://gitlab.com",
  bitbucket: "https://bitbucket.org",
}

/**
 * The HTTPS remote a connected repository is cloned from, built the way the
 * backend's `remoteForSource` builds it, so the same credential reads it.
 */
export function connectedRepositoryRemote(source: DeploymentDraftSource) {
  if (source.mode !== "connected_repository" || !source.repository) return undefined
  const origin =
    source.provider === "gitea"
      ? source.providerBaseUrl?.replace(/\/+$/, "")
      : PROVIDER_ORIGINS[source.provider ?? ""]
  return origin ? `${origin}/${source.repository}.git` : undefined
}

/**
 * The readiness evidence a plan needs before preflight will let it through.
 *
 * Preflight raises `readiness_missing` as a decision for every web and static
 * workload, and a decision blocks the save. Leaving the list empty therefore
 * meant that the most ordinary deployment there is — a repository that serves
 * HTTP — was refused at the last step, pointing at a control buried under
 * Advanced that nobody had been asked about. The check is the right default on
 * its own merits: traffic should not move to a candidate that has not answered
 * once, and both the port and the path are already known here.
 *
 * The check is the one detection read from the source when it found one: a
 * Dockerfile HEALTHCHECK, a declared health route, the budget a slow start
 * needs. Without that, GET / expecting a 2xx failed every healthy API with no
 * page at its root, so detection also says when any answer should count.
 */
export function defaultChecks(
  profile: WorkloadProfile,
  port: number,
  readiness?: DeploymentDetectedReadiness,
): DeploymentConfiguration["checks"] {
  if (profile === "game")
    return [
      {
        // "game_handshake" is a closed check kind the backend's planning
        // model already accepts, but `validateCheckConfiguration` (checks.go)
        // refuses it any port/host/url/command — no protocol handshake is
        // wired in yet, so a plan saved with that kind and a port was
        // rejected outright. "tcp" is a real, executable check and a
        // reasonable readiness signal for a game server: the process is
        // listening on its own port.
        name: "Game port reachable",
        kind: "tcp",
        phase: "readiness",
        required: true,
        config: { port },
      },
    ]
  if ((profile === "web" || profile === "static") && port > 0) {
    const budget = {
      attempts: readiness?.attempts ?? 20,
      timeoutSeconds: 5,
      intervalSeconds: readiness?.intervalSeconds ?? 3,
    }
    if (readiness?.kind === "docker_health")
      return [
        {
          name: "Container health",
          kind: "docker_health",
          phase: "readiness",
          required: true,
          config: budget,
        },
      ]
    return [
      {
        name: "HTTP readiness",
        kind: "http",
        phase: "readiness",
        required: true,
        config: {
          path: readiness?.path ?? "/",
          ...(readiness?.acceptAnyAnswer ? { acceptAnyAnswer: true } : {}),
          ...budget,
        },
      },
    ]
  }
  return []
}

/**
 * The checks a plan carries once its profile and its port are both known.
 *
 * Preflight only allows candidate-first activation, and only requires a
 * readiness gate, for a web or static workload — so the moment one of those
 * has a port it also needs something verifying it. Two controls answer half
 * of that question each: the type Select and the port field. Only the first
 * used to add the check, so a plan that got its port from the second reached
 * Deploy with `readiness_missing` standing over a control nobody had touched.
 */
export function checksForRuntime(
  checks: DeploymentConfiguration["checks"],
  profile: WorkloadProfile,
  port: number,
  readiness?: DeploymentDetectedReadiness,
): DeploymentConfiguration["checks"] {
  if (profile === "worker") return checks.filter((check) => check.phase !== "readiness")
  const gated = profile === "web" || profile === "static"
  if (!gated || port <= 0 || checks.some((check) => check.phase === "readiness")) return checks
  return [...checks, ...defaultChecks(profile, port, readiness)]
}

/**
 * The environment rows a detected candidate opens with: every variable the
 * source was seen reading, with its example as the placeholder and where it
 * was read as the hint. Values stay empty. A row detection set up — a secret
 * the server mints at commit, an address bound to the domain, a documented
 * default — is answered by the plan's declaration (`detectedVariableDeclarations`)
 * and says so; a row left empty with no setup is skipped at submit rather
 * than set to nothing.
 */
export function discoveredEnvironmentRows(
  candidate?: DeploymentDetectionCandidate,
): EnvironmentRow[] {
  const rows = unplannedVariables(candidate).map((variable): EnvironmentRow => ({
    name: variable.name,
    value: "",
    example: variable.example,
    // A registry credential is read by the dependency install alone, and the
    // plan maps it there once it has a value.
    source:
      variable.step === "install"
        ? `${variable.sources[0]} · read by the install`
        : variable.sources[0],
    detected: true,
    ...(variable.setup ? { setup: variable.setup, reason: variable.setupReason } : {}),
    ...(variable.required ? { required: true } : {}),
    ...(variable.browserInlined || browserInlined(variable.name, candidate?.browserPrefixes)
      ? { browser: true }
      : {}),
    ...(variable.localhostIn ? { localhostIn: variable.localhostIn } : {}),
  }))
  // State moved onto a planned volume through a variable the application
  // reads: the plan declares the value that points the file there
  // (`withPersistentVariables`), and the row shows it, filled.
  for (const entry of candidate?.persistentPaths ?? []) {
    if (!entry.target || !entry.variable || !entry.value) continue
    const note = `Keeps it on the volume at ${entry.target}`
    const index = rows.findIndex((row) => row.name === entry.variable)
    if (index < 0)
      rows.push({
        name: entry.variable,
        value: entry.value,
        source: entry.source,
        detected: true,
        note,
      })
    else if (!rows[index].value) rows[index] = { ...rows[index], value: entry.value, note }
  }
  return rows.length ? rows : [{ name: "", value: "" }]
}

/** Whether a detected row still needs the operator: nothing set it up, or only they hold it. */
export function rowNeedsOperator(row: EnvironmentRow) {
  return Boolean(row.detected && !row.value && (!row.setup || row.setup === "paste"))
}

/**
 * The plan's own declarations for what detection set up, so a detected
 * variable arrives configured rather than as a blank row: a self-issued
 * secret the server generates at commit in its framework's shape, a public
 * address that follows the primary domain, a harmless documented default, and
 * a variable the application cannot start without, which is required. A
 * value typed into the row still wins at commit.
 */
export function detectedVariableDeclarations(
  candidate: DeploymentDetectionCandidate | undefined,
  profile: WorkloadProfile,
): DeploymentConfiguration["variables"] {
  const declarations: DeploymentConfiguration["variables"] = []
  for (const variable of candidate?.variables ?? []) {
    // A static site has no runtime to read anything; its values are build input.
    const scopes = profile === "static" ? ["build"] : ["runtime", "build"]
    const required = variable.required || undefined
    if (variable.setup === "generate" && variable.generateLength)
      declarations.push({
        name: variable.name,
        sensitivity: "secret",
        scopes,
        generate: variable.generateLength,
        ...(variable.generateFormat ? { generateFormat: variable.generateFormat } : {}),
      })
    else if (variable.setup === "domain" && variable.domainTemplate)
      declarations.push({
        name: variable.name,
        sensitivity: "plain",
        scopes,
        domainTemplate: variable.domainTemplate,
        value: "",
      })
    else if (variable.setup === "default" && variable.defaultValue)
      declarations.push({
        name: variable.name,
        sensitivity: "plain",
        scopes,
        value: variable.defaultValue,
      })
    else if (required)
      declarations.push({ name: variable.name, sensitivity: "secret", scopes, required: true })
  }
  // What the proxy decides (`networkVariables`) is declared for the names
  // detection did not already set up, such as a trust setting a framework
  // reads without the source naming it.
  const declared = new Set(declarations.map((variable) => variable.name))
  return [
    ...declarations,
    ...networkVariables(candidate).filter((variable) => !declared.has(variable.name)),
  ]
}

/**
 * Browser prefixes for when detection named none: the frameworks' own
 * conventions that no server reads under the same name. SvelteKit's and
 * Astro's bare PUBLIC_ is left out — PUBLIC_URL is as often a server's own
 * address — and counts only where detection saw one of those frameworks.
 */
export const BROWSER_PREFIXES = [
  "NEXT_PUBLIC_",
  "VITE_",
  "REACT_APP_",
  "NUXT_PUBLIC_",
  "EXPO_PUBLIC_",
  "GATSBY_",
  "VUE_APP_",
]

/** Whether a framework compiles this name into the JavaScript every visitor downloads. */
export function browserInlined(name: string, prefixes: string[] = BROWSER_PREFIXES) {
  return prefixes.some((prefix) => name.startsWith(prefix) && name.length > prefix.length)
}

const BIND_NAMES = new Set([
  "HOST",
  "BIND",
  "BIND_HOST",
  "BIND_ADDR",
  "BIND_ADDRESS",
  "LISTEN",
  "LISTEN_HOST",
  "LISTEN_ADDR",
  "LISTEN_ADDRESS",
  "SERVER_HOST",
  "SERVER_ADDRESS",
  "HTTP_BIND",
  "ADDR",
])

function loopbackHost(host: string) {
  const bare = host
    .trim()
    .toLowerCase()
    .replace(/^\[|\]$/g, "")
  return (
    bare === "localhost" ||
    bare.endsWith(".localhost") ||
    bare === "::1" ||
    bare === "0.0.0.0" ||
    bare === "::" ||
    /^127(\.\d{1,3}){3}$/.test(bare)
  )
}

/**
 * Whether a value, read as a connection target, points at loopback — which
 * inside the container is the application itself. The backend's preflight
 * answers the same question; this is the form's early word. A bind address
 * (HOST=0.0.0.0) is a different question and never counts.
 */
export function pointsAtLocalhost(name: string, value: string) {
  const trimmed = value.trim()
  if (!trimmed || trimmed.startsWith("${{") || BIND_NAMES.has(name)) return false
  const scheme = trimmed.replace(/^jdbc:/, "").match(/^[a-z][a-z0-9+.-]*:\/\/([^/?#]*)/i)
  if (scheme)
    return scheme[1]
      .replace(/^.*@/, "")
      .split(",")
      .some((host) => loopbackHost(host.replace(/:\d+$/, "")))
  for (const part of trimmed.split(";")) {
    const [key, setting] = part.split("=")
    if (setting && /^(host|server|data source|address|addr)$/i.test(key.trim()))
      return loopbackHost(setting.split(/[,:]/)[0])
  }
  if (/_(HOST|HOSTNAME|SERVER|ADDR|ADDRESS|HOSTS)$/.test(name))
    return loopbackHost(trimmed.replace(/:\d+$/, ""))
  return false
}

/**
 * The detected variables, less the ones the plan already answers as network
 * variables. A public URL that follows the primary domain stays askable: with
 * no domain planned it has no value, and the row is where one is typed.
 */
function unplannedVariables(candidate?: DeploymentDetectionCandidate) {
  const planned = new Set(
    (candidate?.networkVariables ?? [])
      .filter((variable) => !variable.domainTemplate)
      .map((variable) => variable.name),
  )
  return (candidate?.variables ?? []).filter((variable) => !planned.has(variable.name))
}

/**
 * The rows the environment document carries: every named row with a value,
 * and every row the operator added. A detected row still showing the value
 * the plan itself declares adds nothing, and sent it would store a path as a
 * secret with the typed rows' scopes instead of the declaration's.
 */
export function environmentRowsToSend(
  rows: EnvironmentRow[],
  variables: DeploymentConfiguration["variables"],
) {
  const declared = new Map(variables.map((variable) => [variable.name, variable.value]))
  return rows.filter(
    (row) =>
      row.name.trim() &&
      (!row.detected || row.value) &&
      !(row.detected && row.value && declared.get(row.name) === row.value),
  )
}

/**
 * Folds a re-detection's variables into rows the operator may already have
 * typed into: nothing typed is lost, and a name already present is not
 * listed twice. A lone blank row gives way to the detected ones, and a row
 * still empty takes a value the new detection filled in — the one that
 * moves state onto a volume the plan now carries.
 */
export function mergeDiscoveredRows(current: EnvironmentRow[], discovered: EnvironmentRow[]) {
  const names = new Set(current.map((row) => row.name).filter(Boolean))
  const additions = discovered.filter((row) => row.detected && !names.has(row.name))
  const filled = new Map(
    discovered.filter((row) => row.detected && row.note && row.value).map((row) => [row.name, row]),
  )
  const refills = current.some((row) => row.name && !row.value && filled.has(row.name))
  if (!additions.length && !refills) return current
  const kept = current
    .filter((row) => row.name || row.value)
    .map((row) => {
      const found = !row.value ? filled.get(row.name) : undefined
      return found ? { ...row, value: found.value, note: found.note } : row
    })
  return [...kept, ...additions]
}

export function validateConfiguration(
  configuration: DeploymentConfiguration,
  profile: WorkloadProfile,
) {
  const errors: WizardErrors = {}
  if (!configuration.build.method) errors.buildMethod = "Choose a build method."
  if (configuration.build.pythonVersion && !PYTHON_VERSION.test(configuration.build.pythonVersion))
    errors.pythonVersion = "Use Python 3.10, 3.11, 3.12, 3.13 or 3.14, or leave the version empty."
  const systemPackages = configuration.build.systemPackages ?? []
  if (systemPackages.length > MAX_SYSTEM_PACKAGES)
    errors.systemPackages = `List at most ${MAX_SYSTEM_PACKAGES} system packages.`
  else if (systemPackages.some((name) => !SYSTEM_PACKAGE.test(name)))
    errors.systemPackages =
      "Use Debian package names: lower-case letters, digits and . + - (for example libpq-dev)."
  if (configuration.build.javaVersion && !JAVA_VERSION.test(configuration.build.javaVersion))
    errors.javaVersion = "Use Java 8, 11, 17, 21 or 25, or let the build files decide."
  if (configuration.build.dotnetVersion && !DOTNET_VERSION.test(configuration.build.dotnetVersion))
    errors.dotnetVersion = "Use .NET 8.0, 9.0 or 10.0, or let the project decide."
  if (configuration.build.target && !DOCKERFILE_STAGE.test(configuration.build.target))
    errors.target = "A stage name starts with a letter and has only letters, digits, . _ and -."
  for (const [name, value] of [
    ["internalPort", configuration.runtime.internalPort ?? 0],
    ["hostPort", configuration.runtime.hostPort ?? 0],
  ] as const)
    if (value < 0 || value > 65535) errors[name] = "Use a port from 1 to 65535, or 0 for none."
  const maxBody = configuration.runtime.maxRequestBodyMb ?? 0
  if (maxBody < 0 || maxBody > MAX_REQUEST_BODY_MB)
    errors.maxRequestBodyMb = `Use 1 to ${MAX_REQUEST_BODY_MB} MB, or 0 for the proxy's default (${DEFAULT_REQUEST_BODY_LIMIT}).`
  if (profile === "game" && !configuration.variables.some((variable) => variable.name === "EULA"))
    errors.eula = "Accept the Minecraft EULA before continuing."
  const names = new Set<string>()
  for (const variable of configuration.variables) {
    if (
      !/^[A-Za-z_][A-Za-z0-9_]{0,127}$/.test(variable.name) ||
      names.has(variable.name) ||
      variable.scopes.length === 0
    ) {
      errors.variables = "Variable names must be unique and each variable needs at least one scope."
      break
    }
    names.add(variable.name)
  }
  const variableScopes = new Map(
    configuration.variables.map((variable) => [variable.name, new Set(variable.scopes)]),
  )
  const buildSecrets = configuration.build.secrets ?? []
  if (
    new Set(buildSecrets.map((secret) => secret.variable)).size !== buildSecrets.length ||
    buildSecrets.some(
      (secret) =>
        !/^[A-Za-z_][A-Za-z0-9_]{0,127}$/.test(secret.variable) ||
        !variableScopes.get(secret.variable)?.has("build"),
    )
  )
    errors.buildSecrets = "Each build secret must name one unique variable with Build scope."
  const releaseTasks = configuration.build.releaseTasks ?? []
  if (
    releaseTasks.some(
      (task) =>
        !task.name.trim() ||
        !task.command.trim() ||
        task.timeoutSeconds < 1 ||
        task.timeoutSeconds > 3600 ||
        task.env.some((name) => !variableScopes.get(name)?.has("release_task")),
    )
  )
    errors.releaseTasks =
      "Release tasks need a name, command, 1–3600 second timeout, and Release task-scoped variables."
  else if (
    !buildsReleaseImage(configuration.build.method) &&
    releaseTasks.some((task) => task.runner === "image")
  )
    errors.releaseTasks =
      "A release task that runs in the release image needs a build that produces one; run it in the dashboard's shell instead."
  if (needsStartCommand(configuration.build))
    errors.startCommand =
      "Set the start command the image runs — or, for a static site, its output directory."
  return errors
}

const CONNECTION_FORMATS = new Set(["jdbc", "jdbc-mariadb", "adonet", "mysql2"])

/**
 * A database link rewritten in the connection shape an earlier reference of
 * the same variable asked for (`5.jdbc`): relinking from Settings is the same
 * consumer, and a plain reference would hand a Spring or .NET application an
 * address it refuses. A reference naming another database on the server
 * (`5.url.app_cache`) keeps nothing, since that name belongs to the old one.
 */
export function withPreviousConnectionShape(link: string, previousTarget?: string) {
  const [, format, database] = (previousTarget ?? "").split(".")
  const connection = link.match(/^\$\{\{database\.(\d+)\}\}$/)
  if (!connection || database || !CONNECTION_FORMATS.has(format)) return link
  return `\${{database.${connection[1]}.${format}}}`
}

/** Whether a build method produces an image a release task can run in, as the backend decides. */
export function buildsReleaseImage(method: DeploymentBuildMethod | undefined) {
  return method !== "none" && method !== "legacy_compose"
}

/**
 * Where a new release task runs: the release's image when the build makes
 * one, since only there are the application's toolchain and variables.
 */
export function defaultReleaseTaskRunner(
  method: DeploymentBuildMethod | undefined,
): "image" | undefined {
  return buildsReleaseImage(method) ? "image" : undefined
}

const DOCKERFILE_STAGE = /^[A-Za-z][A-Za-z0-9_.-]{0,127}$/

/**
 * Prefixes whose variables a front-end build inlines into the JavaScript it
 * serves — the backend's `publicBuildPrefixes`.
 */
export const BROWSER_PREFIX = /^(NEXT_PUBLIC_|VITE_|PUBLIC_|NUXT_PUBLIC_|REACT_APP_|EXPO_PUBLIC_)/

/** The Stage field's hint: what leaving it empty does, and the stages detection read. */
export function dockerfileStageHint(stages?: string[]) {
  const base = "Leave empty to build the last stage."
  return stages?.length ? `${base} This Dockerfile's stages: ${stages.join(", ")}.` : base
}

/**
 * Whether a recipe plan is missing the start command its recipe refuses to
 * build without: Python, Deno and PHP always run one, and a JavaScript build
 * runs one unless it has static output for nginx to serve. The screen that
 * owns the field says so, rather than preflight four screens later or the
 * build after Deploy.
 */
export function needsStartCommand(build: DeploymentConfiguration["build"]) {
  if (build.method !== "recipe" || build.startCommand?.trim()) return false
  switch (build.recipe) {
    case "python":
    case "deno":
    case "php":
      return true
    case "node":
      return !build.outputDirectory?.trim()
    default:
      return false
  }
}

/**
 * A Go candidate's main packages as the go command names them — `./cmd/api`
 * — the first few, then how many more: a tools monorepo can have hundreds,
 * and a hint is one line.
 */
export function goMainPackageList(candidate: DeploymentDetectionCandidate | undefined) {
  const mains = candidate?.goMainPackages ?? []
  const shown = mains.slice(0, 8).map((main) => (main === "." ? "." : `./${main}`))
  const more = mains.length - shown.length + (candidate?.goMainPackagesOmitted ?? 0)
  return shown.join(", ") + (more > 0 ? ` and ${more} more` : "")
}

/**
 * What decides a Java build's release while the setting is automatic, and
 * where the build runs from: "pom.xml java.version declares Java 17 · builds
 * :app from .". Undefined for a candidate detected before builds were read.
 */
export function javaVersionReading(candidate: DeploymentDetectionCandidate | undefined) {
  const build = candidate?.javaBuild
  if (!build) return undefined
  const parts: string[] = []
  if (build.pinned) parts.push(`${build.pinnedFrom} pins Java ${build.pinned}`)
  if (build.release)
    parts.push(
      `${build.releaseFrom} declares Java ${build.release}${build.toolchain ? " as a Gradle toolchain" : ""}`,
    )
  if (!parts.length) {
    const release = javaDefaultRelease(build)
    parts.push(
      release === 21
        ? "Nothing declares a release; the recipe builds on Java 21"
        : `Nothing declares a release; the recipe builds on Java ${release}, the newest Gradle ${build.wrapper} runs on`,
    )
  }
  if (build.context)
    parts.push(`builds ${build.module || "the root project"} from ${build.context}`)
  return parts.join(" · ")
}

/**
 * The release a Java build without a declared one builds on — the backend's
 * planJavaToolchain: Java 21, unless the committed Gradle wrapper is older
 * than the 8.5 that runs on it.
 */
function javaDefaultRelease(build: DeploymentDetectedJavaBuild) {
  if (!build.wrapperUsable || !build.wrapper) return 21
  const [major = 0, minor = 0] = build.wrapper.split(".").map(Number)
  const atLeast = (wantMajor: number, wantMinor: number) =>
    major > wantMajor || (major === wantMajor && minor >= wantMinor)
  if (atLeast(8, 5)) return 21
  if (atLeast(7, 3)) return 17
  if (atLeast(5, 0)) return 11
  return 8
}

/**
 * What decides a .NET project's release and SDK while the setting is
 * automatic: its target frameworks and the SDK global.json pins.
 */
export function dotnetVersionReading(candidate: DeploymentDetectionCandidate | undefined) {
  const build = candidate?.dotnetBuild
  if (!build) return undefined
  const parts = [
    build.targetText
      ? `${build.project} targets ${build.targetText}`
      : `${build.project} declares no target framework`,
  ]
  if (build.sdkPin) parts.push(`${build.sdkPinFrom} pins SDK ${build.sdkPin}`)
  if (build.context) parts.push(`published from ${build.context}`)
  return parts.join(" · ")
}

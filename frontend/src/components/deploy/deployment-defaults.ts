import type {
  DeploymentBuildMethod,
  DeploymentConfiguration,
  DeploymentDetection,
  DeploymentDetectionCandidate,
  DeploymentDraftSource,
  DeploymentSourceMode,
  WorkloadProfile,
} from "@/lib/types"

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

export function defaultConfiguration(
  profile: WorkloadProfile,
  candidate?: DeploymentDetectionCandidate,
  source?: DeploymentDraftSource,
  detection?: DeploymentDetection,
): DeploymentConfiguration {
  const game = profile === "game"
  const image = source?.image ?? (game ? "itzg/minecraft-server:java21" : "")
  const method: DeploymentBuildMethod =
    candidate?.buildMethod ??
    (source?.kind === "image" ? "image" : source?.kind === "compose" ? "compose" : "none")
  const port = candidate?.port ?? (game ? 25565 : profile === "web" ? 3000 : 0)
  const composeVariables = detection?.compose?.variables ?? []
  return {
    build: {
      method,
      recipe: method === "recipe" ? candidate?.recipe : undefined,
      rootDirectory: candidate?.root,
      buildCommand: candidate?.buildCommand,
      startCommand: candidate?.startCommand,
      outputDirectory: candidate?.outputDirectory,
      dockerfile: method === "dockerfile" ? "Dockerfile" : undefined,
      noCache: false,
      secrets: [],
      releaseTasks: [],
    },
    runtime: {
      image,
      command: [],
      internalPort: port,
      hostPort: game ? 25565 : 0,
      bindAddress: "127.0.0.1",
      strategy: profile === "web" || profile === "static" ? "blue_green" : "stop_first",
      privileged: false,
      hostNetwork: false,
      capabilities: [],
      devices: [],
      mounts: game ? [{ source: "minecraft-data", target: "/data", ownership: "managed" }] : [],
    },
    variables: [
      ...composeVariables.map((name) => ({
        name,
        sensitivity: "secret" as const,
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
    ],
    dependencies: [],
    checks: defaultChecks(profile, port),
    domains: [],
    autoDeploy: false,
  }
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
 */
function defaultChecks(profile: WorkloadProfile, port: number): DeploymentConfiguration["checks"] {
  if (profile === "game")
    return [
      {
        name: "Minecraft handshake",
        kind: "game_handshake",
        phase: "readiness",
        required: true,
        config: { port: 25565 },
      },
    ]
  if ((profile === "web" || profile === "static") && port > 0)
    return [
      {
        name: "HTTP readiness",
        kind: "http",
        phase: "readiness",
        required: true,
        config: { path: "/", attempts: 20, timeoutSeconds: 5, intervalSeconds: 3 },
      },
    ]
  return []
}

export function validateConfiguration(
  configuration: DeploymentConfiguration,
  profile: WorkloadProfile,
) {
  const errors: WizardErrors = {}
  if (!configuration.build.method) errors.buildMethod = "Choose a build method."
  for (const [name, value] of [
    ["internalPort", configuration.runtime.internalPort ?? 0],
    ["hostPort", configuration.runtime.hostPort ?? 0],
  ] as const)
    if (value < 0 || value > 65535) errors[name] = "Use a port from 1 to 65535, or 0 for none."
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
  return errors
}

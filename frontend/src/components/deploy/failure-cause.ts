import type {
  DeploymentCauseFix,
  DeploymentEngineRun,
  DeploymentFailureCause,
  DeploymentRunSettingsDrift,
  DeploymentSettingsChange,
  DeploymentStep,
  DeploymentSummary,
} from "@/lib/types"
import { sentence, terminalLabel } from "@/components/deploy/vocabulary"

/**
 * Every cause the server can name, in a few words — the same vocabulary as
 * the backend's `causeTitles`, for the run page's notice, the insights'
 * failure groups and anywhere else a terminal code is read out. A code not
 * here is still readable through `sentence`.
 */
export const CAUSE_TITLES: Record<string, string> = {
  build_lockfile_out_of_sync: "Lockfile out of sync",
  build_lockfile_incompatible: "Lockfile from another package-manager release",
  build_package_manager_mismatch: "Package manager differs from the project's",
  build_lifecycle_script_blocked: "Dependency install scripts blocked",
  build_runtime_version: "Language version mismatch",
  build_hugo_extended_required: "Hugo extended edition required",
  build_env_missing: "Variable missing at build",
  build_sqlx_offline: "sqlx has no offline query data",
  build_database_unreachable: "Database unreachable during build",
  build_prerender_failed: "Page failed to prerender",
  build_prisma_client_missing: "Prisma Client not generated",
  build_platform_binary_missing: "Platform binary missing from the lockfile",
  build_legacy_openssl: "Build tool needs legacy OpenSSL",
  build_system_library_missing: "System library missing",
  build_native_toolchain_missing: "Compiler toolchain missing",
  build_install_script_failed: "Dependency install script failed",
  build_php_extension_missing: "PHP extension missing",
  build_dependency_conflict: "Dependency versions conflict",
  build_dependency_advisory_blocked: "Dependency blocked by a security advisory",
  build_dependency_local_path: "Dependency points at a local path",
  build_dependency_unavailable: "Dependency not found",
  build_registry_auth: "Registry refused the credentials",
  build_registry_rate_limited: "Registry rate limit reached",
  build_network: "Network unreachable during build",
  build_base_image_missing: "Base image not found",
  build_image_not_found: "Image not found",
  build_platform_unsupported: "Built for another architecture",
  build_script_crlf: "Script has Windows line endings",
  build_permission: "Script not executable",
  build_wrapper_missing: "Build tool wrapper missing",
  build_embed_source_missing: "go:embed files missing",
  build_dev_dependency_in_production: "Development package loaded in production",
  build_bundle_platform_missing: "Gemfile.lock lacks Linux",
  build_wrong_root: "Wrong root directory",
  build_script_missing: "Build script missing",
  build_module_not_found: "Module not found",
  build_type_error: "Type error",
  build_compile_error: "Compile error",
  build_command_not_found: "Command not found",
  build_output_missing: "Build output missing",
  build_copy_source_missing: "COPY source missing",
  build_out_of_memory: "Build ran out of memory",
  build_disk_full: "Disk full during build",
  build_dockerfile_invalid: "Dockerfile does not parse",
  build_timeout: "Build timed out",
  build_failed: "Build failed",
  builder_missing: "Docker Buildx missing",
  builder_unavailable: "Builder unavailable",
  registry_rate_limited: "Registry rate limit reached",
  registry_unreachable: "Registry unreachable",
  registry_auth_failed: "Registry refused the server",
  base_image_missing: "Base image not found",
  source_auth_failed: "Git credential refused",
  source_repository_missing: "Repository not found",
  source_unreachable: "Git remote unreachable",
  source_revision_unavailable: "Commit no longer on the remote",
  ref_not_found: "Branch or tag not found",
  runtime_port_in_use: "Port already in use",
  image_missing: "Image missing",
  mount_invalid: "Mount cannot be created",
  schema_missing: "Database schema not applied",
  runtime_oom: "Application ran out of memory",
  runtime_prisma_engine_missing: "Prisma engine cannot load",
  runtime_env_missing: "Variable missing at runtime",
  runtime_database_localhost: "Database address points at localhost",
  runtime_host_disallowed: "Host not allowed by the application",
  runtime_entry_missing: "Start command's entry missing",
  runtime_module_missing: "Module not installed",
  runtime_library_missing: "System library missing at runtime",
  runtime_cgo_required: "Binary built without cgo",
  runtime_version: "Language version mismatch",
  runtime_exec_format: "Binary for another architecture",
  runtime_command_not_found: "Start command not found",
  runtime_origin_rejected: "Origin rejected by the application",
  runtime_pidfile_stale: "Stale PID file",
  runtime_schema_push_refused: "Schema push refused",
  runtime_loopback_bind: "Application listens on localhost only",
  runtime_port_mismatch: "Application listens on another port",
  runtime_start_exited: "Start command exited",
  release_migration_failed: "Migration failed",
  release_migration_failed_before: "Earlier migration failed",
  release_database_not_empty: "Database has an unmanaged schema",
  release_database_auth_failed: "Database refused the credentials",
  release_database_unreachable: "Database unreachable from release task",
  release_env_missing: "Variable missing in release task",
  release_task_command_not_found: "Release task command not found",
  release_task_timeout: "Release task timed out",
  release_task_failed: "Release task failed",
  step_timeout: "Step timed out",
}

/** A terminal or step code in the reader's words: its cause title, else the code as a sentence. */
export function causeTitle(code: string) {
  return CAUSE_TITLES[code] ?? sentence(code)
}

/** The codes that say where a run failed rather than why. */
const GENERIC_FAILURES = new Set([
  "build_failed",
  "release_task_failed",
  "health_gate_failed",
  "candidate_start_failed",
])

/**
 * A terminal code under a status that already says the run failed: a named
 * cause by its title, a code that only says where by the place ("Build",
 * "Health gate").
 */
export function failureLabel(code: string) {
  if (GENERIC_FAILURES.has(code) || !CAUSE_TITLES[code]) return terminalLabel(code)
  return CAUSE_TITLES[code]
}

/**
 * The cause as a headline, sharper than its title where the evidence names
 * the object: "package-lock.json is out of sync", "`npm` not found".
 */
export function causeHeadline(cause: DeploymentFailureCause) {
  const subject = cause.subjects?.[0]
  switch (cause.code) {
    case "build_lockfile_out_of_sync":
      return cause.detail ? `${cause.detail} is out of sync` : causeTitle(cause.code)
    case "build_command_not_found":
    case "runtime_command_not_found":
    case "release_task_command_not_found":
      return subject ? `\`${subject}\` not found` : causeTitle(cause.code)
    case "build_env_missing":
      return subject ? `${subject} missing at build` : causeTitle(cause.code)
    case "runtime_env_missing":
      return subject ? `${subject} missing at runtime` : causeTitle(cause.code)
    case "release_env_missing":
      return subject ? `${subject} missing in the release task` : causeTitle(cause.code)
    case "runtime_port_mismatch":
      return subject ? `Application listens on port ${subject}` : causeTitle(cause.code)
    case "schema_missing":
      return cause.table ? `Table ${cause.table} does not exist` : causeTitle(cause.code)
  }
  return causeTitle(cause.code)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

function readCause(value: unknown): DeploymentFailureCause | undefined {
  if (!isRecord(value) || typeof value.code !== "string" || value.code === "") return undefined
  return value as DeploymentFailureCause
}

/**
 * The failed step of a run and the cause its evidence records: a build's or
 * a release task's `cause`, or a health gate's `diagnostics.cause`. The
 * steps are the latest attempts, as the run page reads them.
 */
export function failureCause(
  steps: DeploymentStep[],
): { step: DeploymentStep; cause?: DeploymentFailureCause } | undefined {
  const step = steps.find(
    (candidate) => candidate.state === "failed" || candidate.state === "blocked",
  )
  if (!step) return undefined
  const evidence = step.evidence
  const cause =
    readCause(evidence?.cause) ??
    (isRecord(evidence?.diagnostics) ? readCause(evidence.diagnostics.cause) : undefined)
  return { step, cause }
}

const MANAGER_NAMES: Record<string, string> = { npm: "npm", bun: "Bun", pnpm: "pnpm", yarn: "Yarn" }

/**
 * Where a fix is applied and what its button says: the settings page and
 * section that hold the field, with a variable's name, scope and computed
 * value carried in the address so the editor opens filled in. A fix's value
 * is a constant the server computed — a package manager, a flag — never
 * anything a variable holds.
 */
export function fixTarget(
  projectId: number,
  fix: DeploymentCauseFix,
): { href: string; label: string } | undefined {
  const base = `/deploy/${projectId}/settings`
  const field = fix.field
  if (field.startsWith("variables.")) {
    const name = field.slice("variables.".length)
    const query = new URLSearchParams({ variable: name })
    if (fix.scope) query.set("scope", fix.scope)
    if (fix.kind === "add_variable" && fix.value) query.set("value", fix.value)
    const label =
      fix.kind === "variable_scope"
        ? `Give ${name} the ${fix.scope ?? "needed"} scope`
        : fix.value
          ? `Add ${name}=${fix.value}`
          : `Add ${name}`
    return { href: `${base}/variables?${query}`, label }
  }
  if (field === "dependencies") return { href: `${base}/databases`, label: "Link a database" }
  if (field.startsWith("runtime.")) {
    const setting = field.slice("runtime.".length)
    if (setting === "internalPort" && fix.value)
      return { href: `${base}/runtime#runtime`, label: `Set the port to ${fix.value}` }
    if (setting === "memoryMb")
      return { href: `${base}/runtime#runtime`, label: "Review the memory limit" }
    return { href: `${base}/runtime#runtime`, label: "Review the runtime" }
  }
  const setting = field.replace(/^configuration\.build\.|^build\./, "")
  const commands = `${base}/build#commands`
  switch (setting) {
    case "packageManager":
      return fix.value && MANAGER_NAMES[fix.value]
        ? { href: `${base}/build#build`, label: `Build with ${MANAGER_NAMES[fix.value]}` }
        : { href: `${base}/build#build`, label: "Choose the package manager" }
    case "goVersion":
      return {
        href: `${base}/build#build`,
        label: fix.value ? `Use Go ${fix.value}` : "Choose the Go version",
      }
    case "pythonVersion":
      return {
        href: `${base}/build#build`,
        label: fix.value ? `Use Python ${fix.value}` : "Choose the Python version",
      }
    case "buildCommand":
      return {
        href: commands,
        label: fix.kind === "set_build" ? "Change the build command" : "Review the build command",
      }
    case "startCommand":
      return {
        href: commands,
        label: fix.kind === "set_build" ? "Change the start command" : "Review the start command",
      }
    case "outputDirectory":
      return {
        href: commands,
        label: fix.value
          ? `Set the output directory to ${fix.value}`
          : "Review the output directory",
      }
    case "rootDirectory":
      return { href: commands, label: "Review the root directory" }
    case "releaseTasks":
      return { href: `${base}/build#release-tasks`, label: "Review the release tasks" }
  }
  return undefined
}

/** One changed setting in a few words: "packageManager npm → bun", "DATABASE_URL now also build". */
export function settingsChangeText(change: DeploymentSettingsChange) {
  if (change.kind === "source") return "the source"
  if (change.kind === "variable") {
    if (change.change === "scope") {
      const before = new Set((change.before ?? "").split(",").filter(Boolean))
      const added = (change.after ?? "").split(",").filter((scope) => scope && !before.has(scope))
      return added.length > 0
        ? `${change.field} now available at ${added.join(", ").replace("release_task", "release tasks")}`
        : `${change.field} scopes changed`
    }
    return `${change.field} ${change.change === "changed" ? "value changed" : change.change}`
  }
  if (change.change === "changed" && change.before && change.after) {
    return `${change.field} ${change.before} → ${change.after}`
  }
  if (change.change === "added" && change.after) return `${change.field} set to ${change.after}`
  return `${change.field} ${change.change}`
}

const DRIFT_SHOWN = 3

/** The one line under a failure that says the settings have moved on since the run. */
export function driftLine(drift: DeploymentRunSettingsDrift | undefined) {
  if (!drift?.changed) return undefined
  if (drift.changes.length === 0) return "Settings changed since this run."
  const shown = drift.changes.slice(0, DRIFT_SHOWN).map(settingsChangeText)
  const more = drift.changes.length - shown.length
  return `Settings changed since this run: ${shown.join("; ")}${more > 0 ? `; and ${more} more` : ""}.`
}

/**
 * The request "Deploy with current settings" makes: the run's own commit for
 * a remote Git source, so the new settings are tried on exactly what failed,
 * and a plain deploy for a source a revision does not apply to.
 */
export function deployWithCurrentSettings(
  run: Pick<DeploymentEngineRun, "sourceRevision">,
  deployment: Pick<DeploymentSummary, "sourceKind" | "sourceRemote"> | undefined,
) {
  if (run.sourceRevision && deployment?.sourceKind === "git" && deployment.sourceRemote) {
    return { operation: "deploy", sourceRevision: run.sourceRevision }
  }
  return { operation: "deploy" }
}

/** A failed or cancelled run whose plan is older than the one saved now. */
export function runPlanIsStale(
  run: Pick<DeploymentEngineRun, "planRevision" | "environmentId">,
  deployment: Pick<DeploymentSummary, "desiredRevision" | "environmentId"> | undefined,
) {
  return Boolean(
    deployment &&
    run.environmentId === deployment.environmentId &&
    run.planRevision < deployment.desiredRevision,
  )
}

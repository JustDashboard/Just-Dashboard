import type { DdlRequest } from "@/components/database/schema/changes"
import type { DbPolicy } from "@/components/database/schema/types"

/**
 * What the row-security reading decides without the server: which conditions
 * a command takes, what each template fills a policy form with, the claims a
 * test of a role sets, and which request a form becomes. The statement is the
 * server's — what is written here are the conditions a reader would otherwise
 * have to type, and they are only a start: the form shows them, and the
 * statement the server draws up from them, before anything runs.
 */

export const POLICY_COMMANDS = ["ALL", "SELECT", "INSERT", "UPDATE", "DELETE"] as const
export type PolicyCommand = (typeof POLICY_COMMANDS)[number]

/** The words the page says a command in. */
export const COMMAND_WORD: Record<PolicyCommand, string> = {
  ALL: "Every command",
  SELECT: "Reading",
  INSERT: "Inserting",
  UPDATE: "Updating",
  DELETE: "Deleting",
}

/**
 * Which of the two conditions a command takes. USING says which rows exist
 * for the command; WITH CHECK says which rows it may write. A SELECT or a
 * DELETE writes none, and an INSERT has no row to look at before it.
 */
export function clausesOf(command: PolicyCommand): { using: boolean; withCheck: boolean } {
  return {
    using: command !== "INSERT",
    withCheck: command !== "SELECT" && command !== "DELETE",
  }
}

export type PolicyDraft = {
  name: string
  command: PolicyCommand
  restrictive: boolean
  /** Empty is everybody. */
  roles: string[]
  using: string
  withCheck: string
}

export const BLANK_POLICY: PolicyDraft = {
  name: "",
  command: "ALL",
  restrictive: false,
  roles: [],
  using: "",
  withCheck: "",
}

/** A policy as the form that changes it opens: what it says now. */
export function draftOf(policy: DbPolicy): PolicyDraft {
  return {
    name: policy.name,
    command: policy.command,
    restrictive: !policy.permissive,
    roles: policy.roles,
    using: policy.using ?? "",
    withCheck: policy.withCheck ?? "",
  }
}

/** The conditions a draft sends: the ones its command takes, and that are written. */
function clauses(draft: PolicyDraft) {
  const takes = clausesOf(draft.command)
  const body: { using?: string; withCheck?: string } = {}
  if (takes.using && draft.using.trim() !== "") body.using = draft.using.trim()
  if (takes.withCheck && draft.withCheck.trim() !== "") body.withCheck = draft.withCheck.trim()
  return body
}

/** `POST /ddl/policy`, or null while the policy has no name or no condition. */
export function createPolicyRequest(
  schema: string,
  table: string,
  draft: PolicyDraft,
): DdlRequest | null {
  const written = clauses(draft)
  if (draft.name.trim() === "" || Object.keys(written).length === 0) return null
  return {
    method: "POST",
    path: "/ddl/policy",
    body: {
      schema,
      table,
      name: draft.name.trim(),
      command: draft.command,
      restrictive: draft.restrictive,
      roles: draft.roles,
      ...written,
    },
  }
}

/** Whether a draft says something other than the policy does. */
export function policyChanged(policy: DbPolicy, draft: PolicyDraft): boolean {
  const was = draftOf(policy)
  const sameRoles =
    was.roles.length === draft.roles.length && was.roles.every((role) => draft.roles.includes(role))
  const written = clauses(draft)
  return (
    !sameRoles ||
    (written.using ?? "") !== was.using.trim() ||
    (written.withCheck ?? "") !== was.withCheck.trim()
  )
}

/**
 * `PATCH /ddl/policy`: what the policy applies to and checks, restated. The
 * engine cannot change a policy's command or its kind, and cannot take a
 * condition away, so a condition left empty is left as it is. Null while the
 * draft says what the policy already says.
 */
export function alterPolicyRequest(
  schema: string,
  table: string,
  policy: DbPolicy,
  draft: PolicyDraft,
): DdlRequest | null {
  if (!policyChanged(policy, draft)) return null
  return {
    method: "PATCH",
    path: "/ddl/policy",
    body: { schema, table, name: policy.name, roles: draft.roles, ...clauses(draft) },
  }
}

export function dropPolicyRequest(schema: string, table: string, name: string): DdlRequest {
  return { method: "DELETE", path: "/ddl/policy", body: { schema, table, name } }
}

export type RowSecurityState = { enabled: boolean; forced: boolean }

/**
 * `PATCH /ddl/row-security` for the switches as the reader left them: only
 * what differs from the table is sent. Null while nothing does.
 */
export function rowSecurityRequest(
  schema: string,
  table: string,
  now: RowSecurityState,
  wanted: RowSecurityState,
): DdlRequest | null {
  const body: Record<string, unknown> = { schema, table }
  if (wanted.enabled !== now.enabled) body.enabled = wanted.enabled
  if (wanted.forced !== now.forced) body.force = wanted.forced
  return Object.keys(body).length === 2
    ? null
    : { method: "PATCH", path: "/ddl/row-security", body }
}

/* --------------------------------------------------------------- templates */

export type PolicyTemplateId = "owner" | "owner-setting" | "tenant" | "read-only"

export type TemplateInput = "column" | "setting" | "role"

export type PolicyTemplate = {
  id: PolicyTemplateId
  label: string
  /** What the policy does, in a sentence. */
  says: string
  /** What the template asks for. */
  needs: TemplateInput[]
  /** The setting it reads, where it reads one. */
  setting?: string
}

export const TEMPLATES: PolicyTemplate[] = [
  {
    id: "owner",
    label: "Owner only",
    says: "A role sees and writes the rows whose column holds its own name.",
    needs: ["column"],
  },
  {
    id: "owner-setting",
    label: "Owner by a setting",
    says: "A session sees and writes the rows whose column equals a setting it carries, such as the signed-in user.",
    needs: ["column", "setting"],
    setting: "app.user_id",
  },
  {
    id: "tenant",
    label: "Tenant isolation",
    says: "A session sees and writes only its own tenant's rows, named by a setting.",
    needs: ["column", "setting"],
    setting: "app.tenant_id",
  },
  {
    id: "read-only",
    label: "Read-only for a role",
    says: "The role reads every row. Without a policy of its own for writing, it writes none.",
    needs: ["role"],
  },
]

export type TemplateInputs = { column: string; setting: string; role: string }

/** A setting as a policy reads it: a name with a dot, which is how the server keeps its own apart. */
const SETTING = /^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)+$/

export function settingProblem(setting: string): string | undefined {
  if (setting.trim() === "") return "Name the setting."
  if (!SETTING.test(setting.trim())) {
    return "A setting is a name with a dot in it, such as app.tenant_id."
  }
  return undefined
}

/** What is missing before a template can fill the form, or nothing. */
export function templateProblem(
  template: PolicyTemplate,
  inputs: TemplateInputs,
): string | undefined {
  if (template.needs.includes("column") && inputs.column === "") return "Choose the column."
  if (template.needs.includes("setting")) {
    const problem = settingProblem(inputs.setting)
    if (problem) return problem
  }
  if (template.needs.includes("role") && inputs.role === "") return "Choose the role."
  return undefined
}

/** The pieces of a policy form a template fills; null while it is missing something. */
export function fromTemplate(
  template: PolicyTemplate,
  table: string,
  inputs: TemplateInputs,
  /** Writes a column's name the way the engine quotes one. */
  quote: (name: string) => string,
): Pick<PolicyDraft, "name" | "command" | "roles" | "using" | "withCheck"> | null {
  if (templateProblem(template, inputs)) return null
  const column = quote(inputs.column)
  const setting = inputs.setting.trim()
  switch (template.id) {
    case "owner": {
      const condition = `${column} = current_user`
      return {
        name: `${table}_owner_only`,
        command: "ALL",
        roles: [],
        using: condition,
        withCheck: condition,
      }
    }
    case "owner-setting":
    case "tenant": {
      // The column is read as text so the same policy serves a uuid, an
      // integer and a text key; the second argument makes an unset setting
      // NULL, which matches no row, instead of an error.
      const condition = `${column}::text = current_setting('${setting}', true)`
      return {
        name: template.id === "tenant" ? `${table}_tenant_isolation` : `${table}_owner_by_setting`,
        command: "ALL",
        roles: [],
        using: condition,
        withCheck: condition,
      }
    }
    case "read-only":
      return {
        name: `${table}_${inputs.role}_read_only`,
        command: "SELECT",
        roles: [inputs.role],
        using: "true",
        withCheck: "",
      }
  }
}

/** The column a template most likely means, by what such a column is usually called. */
export function guessColumn(template: PolicyTemplateId, columns: readonly string[]): string {
  const pattern =
    template === "tenant"
      ? /tenant|organi[sz]ation|org_|account|company|workspace/i
      : /owner|user|author|created_by/i
  return columns.find((name) => pattern.test(name)) ?? ""
}

/** Every setting the policies read, in the order they first read it. */
export function settingsRead(policies: readonly DbPolicy[]): string[] {
  const found: string[] = []
  for (const policy of policies) {
    for (const text of [policy.using, policy.withCheck]) {
      for (const match of (text ?? "").matchAll(/current_setting\(\s*'([^']+)'/gi)) {
        if (!found.includes(match[1])) found.push(match[1])
      }
    }
  }
  return found
}

/* ------------------------------------------------------------------ tester */

export type ClaimDraft = {
  /** Stable across edits; never sent. */
  key: string
  name: string
  value: string
}

let serial = 0

export function blankClaim(over: Partial<ClaimDraft> = {}): ClaimDraft {
  serial += 1
  return { key: `k${Date.now().toString(36)}${serial}`, name: "", value: "", ...over }
}

/** A row nobody has typed in is not a claim and not a mistake. */
function emptyClaim(claim: ClaimDraft) {
  return claim.name.trim() === "" && claim.value === ""
}

/** What is wrong with a claim, by its key. */
export function claimProblems(claims: readonly ClaimDraft[]): Record<string, string> {
  const problems: Record<string, string> = {}
  const seen = new Set<string>()
  for (const claim of claims) {
    if (emptyClaim(claim)) continue
    const name = claim.name.trim()
    const problem = settingProblem(name)
    if (problem) problems[claim.key] = problem
    else if (seen.has(name.toLowerCase())) problems[claim.key] = `${name} is set twice.`
    else seen.add(name.toLowerCase())
  }
  return problems
}

export type RoleTestDraft = {
  role: string
  claims: ClaimDraft[]
  query: string
}

/** `POST /rls/test`'s body, or null while the test cannot be asked for. */
export function testBody(
  schema: string,
  table: string,
  draft: RoleTestDraft,
): Record<string, unknown> | null {
  if (draft.role === "" || draft.query.trim() === "") return null
  if (Object.keys(claimProblems(draft.claims)).length > 0) return null
  return {
    role: draft.role,
    schema,
    table,
    query: draft.query.trim(),
    claims: draft.claims
      .filter((claim) => !emptyClaim(claim))
      .map((claim) => ({ name: claim.name.trim(), value: claim.value })),
  }
}

/** The statement a test opens on: the table, as the engine writes its name. */
export function testQuery(quotedTable: string): string {
  return `SELECT * FROM ${quotedTable}`
}

/**
 * How a table's policies read as a sentence about who is held to them: the
 * line under the switches.
 */
export function filterWords(state: {
  enabled: boolean
  forced: boolean
  policies: readonly unknown[]
}): string {
  if (!state.enabled) return "Every row is visible to anyone who may read the table."
  const owner = state.forced
    ? "The owner is held to the policies as well."
    : "The owner is not held to them."
  if (state.policies.length === 0) {
    return `With no policy, no role that is held to them sees a row. ${owner}`
  }
  return owner
}

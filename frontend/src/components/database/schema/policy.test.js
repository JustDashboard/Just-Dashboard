import { describe, expect, test } from "bun:test"
import { spendsBudget } from "./changes"
import {
  BLANK_POLICY,
  TEMPLATES,
  alterPolicyRequest,
  blankClaim,
  claimProblems,
  clausesOf,
  createPolicyRequest,
  draftOf,
  dropPolicyRequest,
  filterWords,
  fromTemplate,
  guessColumn,
  policyChanged,
  rowSecurityRequest,
  settingProblem,
  settingsRead,
  templateProblem,
  testBody,
  testQuery,
} from "./policy"

const quote = (name) => `"${name}"`
const template = (id) => TEMPLATES.find((one) => one.id === id)

const policy = {
  name: "notes_owner_only",
  command: "ALL",
  permissive: true,
  roles: ["web"],
  using: "(owner = (CURRENT_USER)::text)",
  withCheck: "(owner = (CURRENT_USER)::text)",
}

describe("the conditions a command takes", () => {
  test("an insert has nothing to look at, and a read or a delete writes nothing", () => {
    expect(clausesOf("ALL")).toEqual({ using: true, withCheck: true })
    expect(clausesOf("UPDATE")).toEqual({ using: true, withCheck: true })
    expect(clausesOf("SELECT")).toEqual({ using: true, withCheck: false })
    expect(clausesOf("DELETE")).toEqual({ using: true, withCheck: false })
    expect(clausesOf("INSERT")).toEqual({ using: false, withCheck: true })
  })
})

describe("a new policy", () => {
  const draft = { ...BLANK_POLICY, name: " mine ", using: " owner = current_user " }

  test("is asked about once it has a name and a condition", () => {
    expect(createPolicyRequest("app", "notes", BLANK_POLICY)).toBeNull()
    expect(createPolicyRequest("app", "notes", { ...draft, name: "" })).toBeNull()
    expect(createPolicyRequest("app", "notes", { ...draft, using: "  " })).toBeNull()
    expect(createPolicyRequest("app", "notes", draft)).toEqual({
      method: "POST",
      path: "/ddl/policy",
      body: {
        schema: "app",
        table: "notes",
        name: "mine",
        command: "ALL",
        restrictive: false,
        roles: [],
        using: "owner = current_user",
      },
    })
  })

  test("sends only the conditions its command takes", () => {
    const both = { ...draft, withCheck: "true" }
    expect(createPolicyRequest("app", "notes", { ...both, command: "SELECT" }).body).toMatchObject({
      using: "owner = current_user",
    })
    expect(
      createPolicyRequest("app", "notes", { ...both, command: "SELECT" }).body,
    ).not.toHaveProperty("withCheck")
    const insert = createPolicyRequest("app", "notes", { ...both, command: "INSERT" }).body
    expect(insert).toMatchObject({ withCheck: "true" })
    expect(insert).not.toHaveProperty("using")
    // A condition typed for another command and left behind is not a condition.
    expect(createPolicyRequest("app", "notes", { ...draft, command: "INSERT" })).toBeNull()
  })

  test("says restrictive and the roles it applies to", () => {
    const body = createPolicyRequest("app", "notes", {
      ...draft,
      restrictive: true,
      roles: ["web", "worker"],
    }).body
    expect(body).toMatchObject({ restrictive: true, roles: ["web", "worker"] })
  })
})

describe("changing a policy", () => {
  test("is not asked about while the form says what the policy says", () => {
    expect(policyChanged(policy, draftOf(policy))).toBe(false)
    expect(alterPolicyRequest("app", "notes", policy, draftOf(policy))).toBeNull()
    // The roles are a set: the order they were chosen in is not a change.
    const two = { ...policy, roles: ["a", "b"] }
    expect(policyChanged(two, { ...draftOf(two), roles: ["b", "a"] })).toBe(false)
  })

  test("restates the roles and the conditions, under the policy's own name", () => {
    const edit = { ...draftOf(policy), roles: [], using: "owner = 'x'" }
    expect(alterPolicyRequest("app", "notes", policy, edit)).toEqual({
      method: "PATCH",
      path: "/ddl/policy",
      body: {
        schema: "app",
        table: "notes",
        name: "notes_owner_only",
        roles: [],
        using: "owner = 'x'",
        withCheck: "(owner = (CURRENT_USER)::text)",
      },
    })
  })

  test("leaves a condition out that the policy's command does not take", () => {
    const insert = { ...policy, command: "INSERT", using: "" }
    const body = alterPolicyRequest("app", "notes", insert, {
      ...draftOf(insert),
      using: "true",
      withCheck: "false",
    }).body
    expect(body).not.toHaveProperty("using")
    expect(body.withCheck).toBe("false")
  })

  test("is a DELETE for a drop, which spends the destructive budget", () => {
    const request = dropPolicyRequest("app", "notes", "mine")
    expect(request).toEqual({
      method: "DELETE",
      path: "/ddl/policy",
      body: { schema: "app", table: "notes", name: "mine" },
    })
    expect(spendsBudget(request)).toBe(true)
  })
})

describe("the switches", () => {
  const now = { enabled: false, forced: false }

  test("send only what differs from the table", () => {
    expect(rowSecurityRequest("app", "notes", now, now)).toBeNull()
    expect(rowSecurityRequest("app", "notes", now, { enabled: true, forced: false }).body).toEqual({
      schema: "app",
      table: "notes",
      enabled: true,
    })
    expect(
      rowSecurityRequest(
        "app",
        "notes",
        { enabled: true, forced: false },
        { enabled: true, forced: true },
      ).body,
    ).toEqual({
      schema: "app",
      table: "notes",
      force: true,
    })
    expect(
      rowSecurityRequest(
        "app",
        "notes",
        { enabled: true, forced: true },
        { enabled: false, forced: false },
      ).body,
    ).toEqual({ schema: "app", table: "notes", enabled: false, force: false })
  })

  test("a change that is a PATCH does not spend the destructive budget at a preview", () => {
    expect(
      spendsBudget(rowSecurityRequest("app", "notes", now, { enabled: true, forced: false })),
    ).toBe(false)
  })

  test("are said as a sentence about who is held", () => {
    expect(filterWords({ enabled: false, forced: false, policies: [] })).toContain("Every row")
    expect(filterWords({ enabled: true, forced: false, policies: [] })).toContain("no role")
    expect(filterWords({ enabled: true, forced: true, policies: [1] })).toBe(
      "The owner is held to the policies as well.",
    )
    expect(filterWords({ enabled: true, forced: false, policies: [1] })).toBe(
      "The owner is not held to them.",
    )
  })
})

describe("the templates", () => {
  const inputs = { column: "owner", setting: "app.tenant_id", role: "auditor" }

  test("owner only compares a column with the signed-in role", () => {
    expect(fromTemplate(template("owner"), "notes", inputs, quote)).toEqual({
      name: "notes_owner_only",
      command: "ALL",
      roles: [],
      using: '"owner" = current_user',
      withCheck: '"owner" = current_user',
    })
  })

  test("a setting makes the comparison against current_setting, as text, and an unset one matches no row", () => {
    const filled = fromTemplate(template("tenant"), "notes", inputs, quote)
    expect(filled.using).toBe(`"owner"::text = current_setting('app.tenant_id', true)`)
    expect(filled.withCheck).toBe(filled.using)
    expect(filled.name).toBe("notes_tenant_isolation")
    expect(
      fromTemplate(template("owner-setting"), "notes", { ...inputs, setting: "app.user_id" }, quote)
        .name,
    ).toBe("notes_owner_by_setting")
  })

  test("read-only for a role is a SELECT policy for that role alone", () => {
    expect(fromTemplate(template("read-only"), "notes", inputs, quote)).toEqual({
      name: "notes_auditor_read_only",
      command: "SELECT",
      roles: ["auditor"],
      using: "true",
      withCheck: "",
    })
  })

  test("are not filled in while something they ask for is missing", () => {
    expect(templateProblem(template("owner"), { ...inputs, column: "" })).toBe("Choose the column.")
    expect(templateProblem(template("tenant"), { ...inputs, setting: "tenant" })).toContain("a dot")
    expect(templateProblem(template("tenant"), { ...inputs, setting: "x'); DROP" })).toContain(
      "a dot",
    )
    expect(templateProblem(template("read-only"), { ...inputs, role: "" })).toBe("Choose the role.")
    expect(fromTemplate(template("tenant"), "notes", { ...inputs, setting: "" }, quote)).toBeNull()
    expect(templateProblem(template("owner"), { ...inputs, setting: "" })).toBeUndefined()
  })

  test("the column a template means is guessed from what such columns are called", () => {
    const columns = ["id", "tenant_id", "created_by", "body"]
    expect(guessColumn("tenant", columns)).toBe("tenant_id")
    expect(guessColumn("owner", columns)).toBe("created_by")
    expect(guessColumn("owner-setting", ["id", "body"])).toBe("")
  })

  test("every setting a policy reads is found, once", () => {
    const read = settingsRead([
      {
        ...policy,
        using: "t = current_setting('app.tenant_id', true)",
        withCheck: "t = current_setting( 'app.tenant_id' )",
      },
      {
        ...policy,
        using: "u = CURRENT_SETTING('request.jwt.claims')::json ->> 'sub'",
        withCheck: undefined,
      },
      { ...policy, using: "true", withCheck: "" },
    ])
    expect(read).toEqual(["app.tenant_id", "request.jwt.claims"])
  })
})

describe("a test of a role", () => {
  test("accepts a setting of its own, with a dot, and not the role knob", () => {
    expect(settingProblem("app.tenant_id")).toBeUndefined()
    expect(settingProblem("request.jwt.claims")).toBeUndefined()
    expect(settingProblem("role")).toContain("dot")
    expect(settingProblem("  ")).toBe("Name the setting.")
  })

  test("names the claim that is wrong, and a duplicate", () => {
    const good = blankClaim({ name: "app.tenant_id", value: "acme" })
    const bad = blankClaim({ name: "role", value: "postgres" })
    const twice = blankClaim({ name: "APP.TENANT_ID", value: "x" })
    const empty = blankClaim()
    expect(claimProblems([good, empty])).toEqual({})
    expect(Object.keys(claimProblems([good, bad, twice, empty]))).toEqual([bad.key, twice.key])
    expect(claimProblems([good, twice])[twice.key]).toContain("twice")
  })

  test("is asked for once it has a role, a statement and claims that hold", () => {
    const claim = blankClaim({ name: " app.tenant_id ", value: " acme" })
    const draft = { role: "web", query: " SELECT 1 ", claims: [claim, blankClaim()] }
    expect(testBody("app", "notes", { ...draft, role: "" })).toBeNull()
    expect(testBody("app", "notes", { ...draft, query: " " })).toBeNull()
    expect(
      testBody("app", "notes", { ...draft, claims: [blankClaim({ name: "role", value: "x" })] }),
    ).toBeNull()
    expect(testBody("app", "notes", draft)).toEqual({
      role: "web",
      schema: "app",
      table: "notes",
      query: "SELECT 1",
      // The name is trimmed; the value is the operator's, spaces and all.
      claims: [{ name: "app.tenant_id", value: " acme" }],
    })
  })

  test("opens on the table", () => {
    expect(testQuery('"app"."notes"')).toBe('SELECT * FROM "app"."notes"')
  })
})

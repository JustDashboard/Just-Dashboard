import { expect, test } from "bun:test"
import { AUDIT_SECTIONS, actorName, auditSection, isMachine } from "./marks"

test("every action is drawn as the part of the product it touched", () => {
  const cases = {
    "docker.container.restart": "docker",
    "post:docker.containers": "docker",
    "deploy.git.change": "deploy",
    game_command: "deploy",
    "database.ddl.create_table": "database",
    "file.write": "files",
    "terminal.create": "terminal",
    "git.clone": "git",
    "github.pull.merge": "github",
    "certificates.renew": "certificates",
    "fail2ban.unban.all": "intrusion",
    ban: "intrusion",
    unban: "intrusion",
    "firewall.rule.add": "firewall",
    "system.packages.install": "packages",
    "system.updates.apply": "packages",
    "system.user.sshkey.add": "users",
    "systemd.daemon-reload": "services",
    "auth.login": "signin",
    "account.avatar.update": "account",
    "dashboard.user.create": "dashboard",
    logs: "logs",
  }
  for (const [action, key] of Object.entries(cases)) {
    expect({ action, key: auditSection(action)?.key }).toEqual({ action, key })
  }
  expect(auditSection("container.restart")).toBeUndefined()
  expect(auditSection(undefined)).toBeUndefined()
})

test("a section's filter matches every action it draws", () => {
  const samples = {
    signin: "auth.login",
    docker: "docker.container.restart",
    deploy: "deploy.run",
    database: "database.query",
    files: "file.write",
    terminal: "terminal.create",
    git: "git.clone",
    github: "github.signin",
    intrusion: "fail2ban.unban",
    packages: "system.packages.install",
    users: "system.user.create",
  }
  for (const [key, action] of Object.entries(samples)) {
    const section = AUDIT_SECTIONS.find((s) => s.key === key)
    expect({ key, matches: action.includes(section.filter) }).toEqual({ key, matches: true })
  }
})

test("who acted is named by the account, else by what acted without one", () => {
  expect(actorName({ actor: "session", username: "mira" })).toBe("mira")
  expect(actorName({ actor: "webhook", username: "" })).toBe("webhook")
  expect(actorName({ actor: "system", username: "" })).toBe("the dashboard")
  expect(actorName({ actor: "anonymous", username: "" })).toBe("anonymous")
  expect(isMachine({ actor: "reconciler" })).toBe(true)
  expect(isMachine({ actor: "token" })).toBe(false)
})

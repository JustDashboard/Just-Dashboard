import { describe, expect, test } from "bun:test"
import { appendJob, removeJob, replaceJob, toggleJob } from "./crontab"

const raw = [
  "MAILTO=ops@example.com",
  "",
  "# nightly backup",
  "0 3 * * * /usr/local/bin/backup",
  "# 0 4 * * * /usr/local/bin/prune",
  "",
].join("\n")

const backup = {
  line: 4,
  schedule: "0 3 * * *",
  command: "/usr/local/bin/backup",
  comment: "nightly backup",
  raw: "0 3 * * * /usr/local/bin/backup",
  disabled: false,
}
const prune = {
  line: 5,
  schedule: "0 4 * * *",
  command: "/usr/local/bin/prune",
  raw: "# 0 4 * * * /usr/local/bin/prune",
  disabled: true,
}

// Every edit is to one job's own lines. The environment line, the blank line
// and the other job must come through every operation untouched, or the row
// controls and the text editor stop being two views of the same file.
describe("crontab line edits", () => {
  test("disable comments the line out and enable uncomments it", () => {
    expect(toggleJob(raw, backup)).toBe(
      "MAILTO=ops@example.com\n\n# nightly backup\n# 0 3 * * * /usr/local/bin/backup\n# 0 4 * * * /usr/local/bin/prune\n",
    )
    expect(toggleJob(raw, prune)).toBe(
      "MAILTO=ops@example.com\n\n# nightly backup\n0 3 * * * /usr/local/bin/backup\n0 4 * * * /usr/local/bin/prune\n",
    )
  })
  test("remove takes the job's comment with it and nothing else", () => {
    expect(removeJob(raw, backup)).toBe(
      "MAILTO=ops@example.com\n\n# 0 4 * * * /usr/local/bin/prune\n",
    )
    expect(removeJob(raw, prune)).toBe(
      "MAILTO=ops@example.com\n\n# nightly backup\n0 3 * * * /usr/local/bin/backup\n",
    )
  })
  test("replace rewrites the schedule, command and comment in place", () => {
    expect(
      replaceJob(raw, backup, {
        schedule: "30 2 * * 1-5",
        command: "/usr/local/bin/backup --full",
        comment: "weekday backup",
      }),
    ).toBe(
      "MAILTO=ops@example.com\n\n# weekday backup\n30 2 * * 1-5 /usr/local/bin/backup --full\n# 0 4 * * * /usr/local/bin/prune\n",
    )
    // Dropping the comment drops its line; a disabled edit lands commented.
    expect(
      replaceJob(raw, backup, { schedule: "@daily", command: "/bin/true", disabled: true }),
    ).toBe("MAILTO=ops@example.com\n\n# @daily /bin/true\n# 0 4 * * * /usr/local/bin/prune\n")
  })
  test("append adds at the end and starts an empty crontab cleanly", () => {
    expect(
      appendJob(raw, { schedule: "*/5 * * * *", command: "/bin/ping", comment: "keepalive" }),
    ).toBe(`${raw}# keepalive\n*/5 * * * * /bin/ping\n`)
    expect(appendJob("", { schedule: "@reboot", command: "/srv/start.sh" })).toBe(
      "@reboot /srv/start.sh\n",
    )
  })
})

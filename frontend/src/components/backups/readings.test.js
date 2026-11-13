import { expect, test } from "bun:test"
import { backupFacts, backupStates, holdingReading } from "./readings"

const HOUR = 3_600_000
const at = (hours) => new Date(Date.now() + hours * HOUR).toISOString()
const job = (fields) => ({
  id: 1,
  name: "Nginx configuration",
  schedule: "0 3 * * *",
  enabled: true,
  stored: { runs: 7, bytes: 51_500 },
  lastRun: { status: "success", startedAt: at(-4), duration: "48s" },
  nextRun: at(20),
  ...fields,
})
const labels = (jobs) => backupStates(jobs).map((state) => `${state.tone}: ${state.label}`)

test("the page's state leads with what failed, then what is being taken", () => {
  expect(labels([])).toEqual(["stopped: No jobs"])
  expect(labels([job({})])).toEqual(["running: Every job backed up"])
  expect(
    labels([
      job({}),
      job({ id: 2, lastRun: { status: "failed", startedAt: at(-5) } }),
      job({ id: 3, lastRun: { status: "running", startedAt: at(0) } }),
    ]),
  ).toEqual(["danger: 1 job failed", "running: Backing up now"])
  expect(backupStates([job({ lastRun: { status: "running", startedAt: at(0) } })])[0].live).toBe(
    true,
  )
})

test("a job that has never run, a paused one and an imported one are said for what they are", () => {
  expect(labels([job({}), job({ id: 2, lastRun: undefined })])).toEqual([
    "warning: 1 job never run",
  ])
  expect(labels([job({ lastRun: undefined })])).toEqual(["stopped: Nothing backed up yet"])
  expect(labels([job({}), job({ id: 2, enabled: false })])).toEqual([
    "running: Every job backed up",
    "stopped: 1 paused",
  ])
  // An imported job is another server's record: it neither fails nor lands here.
  expect(
    labels([job({ imported: true, lastRun: { status: "failed", startedAt: at(-1) } })]),
  ).toEqual(["stopped: Imported only"])
})

test("the facts are the jobs, what they hold and the next run of one that runs here", () => {
  const facts = backupFacts([
    job({}),
    job({ id: 2, stored: { runs: 3, bytes: 2_000_000 }, nextRun: at(2) }),
    job({ id: 3, enabled: false, nextRun: at(1) }),
    job({ id: 4, imported: true, nextRun: at(0.5), stored: { runs: 0, bytes: 0 } }),
  ])
  expect(facts.jobs).toBe("4 jobs")
  expect(facts.stored).toMatch(/^2\.\d MB in 17 archives$/)
  expect(facts.next).toMatch(/^next backup 2h( 0m)? from now$|^next backup 1h 59m from now$/)
  expect(backupFacts([job({ schedule: "", nextRun: undefined })]).next).toBe("nothing scheduled")
  expect(backupFacts([]).next).toBeUndefined()
  expect(backupFacts([job({ stored: { runs: 0, bytes: 0 } })]).stored).toBe("nothing stored yet")
})

test("what a job holds is said with how much is kept and the last archive's size", () => {
  expect(holdingReading(job({ retention: 7, retentionDays: 0 }), 1000)).toEqual({
    words: "50.3 KB in 7 archives",
    hint: "Keeps last 7 archives · last archive 1000 B",
  })
  expect(
    holdingReading(job({ retention: 0, retentionDays: 0, stored: { runs: 0, bytes: 0 } })),
  ).toEqual({ words: "Nothing stored yet", hint: "Keeps everything" })
  // An imported job's archives are another server's to prune.
  expect(holdingReading(job({ imported: true, retention: 7, retentionDays: 0 }), 2048).hint).toBe(
    "Never pruned from here · last archive 2.0 KB",
  )
})

import { describe, expect, test } from "bun:test"
import { quietNotes } from "./stat-notes"

const DENIED =
  "(Error 1142 (42000): SELECT command denied to user 'jdtest'@'10.0.0.1' for table `performance_schema`.`table_io_waits_summary_by_index_usage`)"

describe("what the engine could not say", () => {
  test("the driver's own error is taken out of the sentence", () => {
    expect(
      quietNotes([
        `Read and write counts are unavailable: this account may not read performance_schema ${DENIED}.`,
      ]),
    ).toEqual([
      "Read and write counts are unavailable: this account may not read performance_schema.",
    ])
    expect(
      quietNotes([
        `Use counts are unavailable: this account may not read performance_schema ${DENIED}, so no index can be called unused.`,
      ]),
    ).toEqual([
      "Use counts are unavailable: this account may not read performance_schema, so no index can be called unused.",
    ])
  })

  test("a line two routes both sent is said once, and a plain line is left alone", () => {
    expect(
      quietNotes(
        ["Index sizes need read access to mysql.innodb_index_stats."],
        [
          "Index sizes need read access to mysql.innodb_index_stats.",
          "Sizes are estimates (dbstat).",
        ],
        undefined,
      ),
    ).toEqual([
      "Index sizes need read access to mysql.innodb_index_stats.",
      "Sizes are estimates (dbstat).",
    ])
  })

  test("a bracket that never closes takes the rest of the line with it", () => {
    expect(quietNotes(["Counts are unavailable (ERROR: permission denied for"])).toEqual([
      "Counts are unavailable",
    ])
  })
})

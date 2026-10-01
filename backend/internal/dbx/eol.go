package dbx

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// When each engine's releases stop being maintained.
//
// A server past that date still runs, and that is the trouble: nothing
// changes on the day, and the next vulnerability found in it is never fixed.
// The dates are the vendors' published ones. They are a table rather than a
// lookup because the dashboard is installed on servers with no route to the
// internet, and because a finding that depends on a third party's API being
// up is a finding that goes missing on the day it matters.
//
// Only releases whose date is published are listed. A version that is not in
// the table produces no finding: saying nothing about a release the table
// does not know is right, and guessing its date is not.

// EndOfLife is one release line's last day of maintenance.
type EndOfLife struct {
	// Product is the engine as its vendor names it.
	Product string `json:"product"`
	// Release is the release line the date belongs to: "13", "8.0", "2016".
	Release string    `json:"release"`
	Date    time.Time `json:"date"`
	// Past is true once the date has gone by.
	Past bool `json:"past"`
	// DaysLeft is how long until the date; negative once it has passed.
	DaysLeft int `json:"daysLeft"`
}

func eolDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic("dbx: bad end-of-life date " + s)
	}
	return t
}

var (
	// PostgreSQL retires a major five years after its release, each November.
	postgresEOL = map[string]time.Time{
		"9.6": eolDate("2021-11-11"), "10": eolDate("2022-11-10"), "11": eolDate("2023-11-09"),
		"12": eolDate("2024-11-14"), "13": eolDate("2025-11-13"), "14": eolDate("2026-11-12"),
		"15": eolDate("2027-11-11"), "16": eolDate("2028-11-09"), "17": eolDate("2029-11-08"),
		"18": eolDate("2030-11-14"),
	}
	// MySQL: the end of extended support for the long-lived series, and the
	// day the next release replaced each short-lived 8.x innovation release.
	mysqlEOL = map[string]time.Time{
		"5.5": eolDate("2018-12-31"), "5.6": eolDate("2021-02-05"), "5.7": eolDate("2023-10-31"),
		"8.0": eolDate("2026-04-30"), "8.1": eolDate("2023-10-25"), "8.2": eolDate("2024-01-16"),
		"8.3": eolDate("2024-04-30"), "8.4": eolDate("2032-04-30"),
	}
	// MariaDB: long-term series are kept five years (three from 11.8 on), the
	// rest until the following release.
	mariadbEOL = map[string]time.Time{
		"5.5": eolDate("2020-04-11"), "10.0": eolDate("2019-03-31"), "10.1": eolDate("2020-10-17"),
		"10.2": eolDate("2022-05-23"), "10.3": eolDate("2023-05-25"), "10.4": eolDate("2024-06-18"),
		"10.5": eolDate("2025-06-24"), "10.6": eolDate("2026-07-06"), "10.7": eolDate("2023-02-09"),
		"10.8": eolDate("2023-05-20"), "10.9": eolDate("2023-08-22"), "10.10": eolDate("2023-11-17"),
		"10.11": eolDate("2028-02-16"), "11.0": eolDate("2024-06-06"), "11.1": eolDate("2024-08-21"),
		"11.2": eolDate("2024-11-21"), "11.4": eolDate("2029-05-29"), "11.8": eolDate("2028-06-04"),
	}
	// SQL Server: the end of extended support, by the year in the product name.
	mssqlEOL = map[string]time.Time{
		"2008": eolDate("2019-07-09"), "2012": eolDate("2022-07-12"), "2014": eolDate("2024-07-09"),
		"2016": eolDate("2026-07-14"), "2017": eolDate("2027-10-12"), "2019": eolDate("2030-01-08"),
		"2022": eolDate("2033-01-11"),
	}
	// Oracle: the last day of any error correction for the release.
	oracleEOL = map[string]time.Time{
		"11": eolDate("2020-12-31"), "12": eolDate("2022-07-31"), "18": eolDate("2021-06-30"),
		"19": eolDate("2032-12-31"), "21": eolDate("2027-07-31"), "23": eolDate("2031-12-31"),
	}

	eolVersionNumber = regexp.MustCompile(`(\d+)\.(\d+)`)
	mssqlYear        = regexp.MustCompile(`SQL Server (\d{4})`)
	oracleRelease    = regexp.MustCompile(`(?i)Database (\d+)[a-z]`)
)

// VersionEndOfLife looks a server's version string up in the table. The
// string is whatever the engine's own version query returned; ok is false
// when the release is not one the table knows.
func VersionEndOfLife(driver Driver, version string, now time.Time) (*EndOfLife, bool) {
	var (
		product, release string
		date             time.Time
		ok               bool
	)
	major, minor, found := leadingVersion(version)
	switch driver {
	case DriverPostgres:
		product = "PostgreSQL"
		if !found {
			return nil, false
		}
		// From 10 on the first number alone is the major; before it, two.
		release = strconv.Itoa(major)
		if major < 10 {
			release += "." + strconv.Itoa(minor)
		}
		date, ok = postgresEOL[release]
	case DriverMySQL:
		if !found {
			return nil, false
		}
		release = strconv.Itoa(major) + "." + strconv.Itoa(minor)
		if strings.Contains(strings.ToLower(version), "mariadb") {
			product = "MariaDB"
			date, ok = mariadbEOL[release]
		} else {
			product = "MySQL"
			date, ok = mysqlEOL[release]
		}
	case DriverMSSQL:
		product = "SQL Server"
		if m := mssqlYear.FindStringSubmatch(version); m != nil {
			release = m[1]
			date, ok = mssqlEOL[release]
		}
	case DriverOracle:
		product = "Oracle Database"
		if m := oracleRelease.FindStringSubmatch(version); m != nil {
			release = m[1]
			date, ok = oracleEOL[release]
		}
	case DriverClickHouse:
		product = "ClickHouse"
		// ClickHouse numbers a release by the year and month it came out,
		// and maintains none for longer than a year: the stable releases for
		// three months, a long-term one for twelve.
		if !found || major < 18 || minor < 1 || minor > 12 {
			return nil, false
		}
		release = strconv.Itoa(major) + "." + strconv.Itoa(minor)
		date = time.Date(2000+major, time.Month(minor), 1, 0, 0, 0, 0, time.UTC).AddDate(1, 0, 0)
		ok = true
	}
	if !ok {
		return nil, false
	}
	days := int(date.Sub(now).Hours() / 24)
	return &EndOfLife{Product: product, Release: release, Date: date, Past: now.After(date), DaysLeft: days}, true
}

// leadingVersion finds the first major.minor pair in a version string.
func leadingVersion(version string) (major, minor int, ok bool) {
	m := eolVersionNumber.FindStringSubmatch(version)
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

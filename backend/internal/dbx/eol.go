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
//
// It is read in two places, and in both as a plain fact: beside each release
// the provisioning catalogue offers, and beside the version a database's
// own pages name.

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
		"12": eolDate("2024-11-21"), "13": eolDate("2025-11-13"), "14": eolDate("2026-11-12"),
		"15": eolDate("2027-11-11"), "16": eolDate("2028-11-09"), "17": eolDate("2029-11-08"),
		"18": eolDate("2030-11-14"),
	}
	// MySQL: the end of extended support for the long-lived series, and the
	// day the next release replaced each short-lived 8.x innovation release.
	mysqlEOL = map[string]time.Time{
		"5.5": eolDate("2018-12-31"), "5.6": eolDate("2021-02-05"), "5.7": eolDate("2023-10-31"),
		"8.0": eolDate("2026-04-30"), "8.1": eolDate("2023-10-25"), "8.2": eolDate("2024-01-16"),
		"8.3": eolDate("2024-04-30"), "8.4": eolDate("2032-04-30"), "9.0": eolDate("2024-10-15"),
		"9.1": eolDate("2025-01-21"), "9.2": eolDate("2025-04-15"), "9.3": eolDate("2025-07-22"),
		"9.4": eolDate("2025-10-21"), "9.5": eolDate("2026-01-20"), "9.6": eolDate("2026-04-21"),
		"9.7": eolDate("2034-04-30"),
	}
	// MariaDB: long-term series are kept five years (three from 11.8 on), the
	// rest until the following release — a day known only once that release
	// is out, so the newest rolling one (13.0) has none yet.
	mariadbEOL = map[string]time.Time{
		"5.5": eolDate("2020-04-11"), "10.0": eolDate("2019-03-31"), "10.1": eolDate("2020-10-17"),
		"10.2": eolDate("2022-05-23"), "10.3": eolDate("2023-05-25"), "10.4": eolDate("2024-06-18"),
		"10.5": eolDate("2025-06-24"), "10.6": eolDate("2026-07-06"), "10.7": eolDate("2023-02-09"),
		"10.8": eolDate("2023-05-20"), "10.9": eolDate("2023-08-22"), "10.10": eolDate("2023-11-17"),
		"10.11": eolDate("2028-02-16"), "11.0": eolDate("2024-06-06"), "11.1": eolDate("2024-08-21"),
		"11.2": eolDate("2024-11-21"), "11.3": eolDate("2024-05-29"), "11.4": eolDate("2029-05-29"),
		"11.5": eolDate("2024-11-21"), "11.6": eolDate("2025-02-13"), "11.7": eolDate("2025-05-12"),
		"11.8": eolDate("2028-06-04"), "12.0": eolDate("2025-11-18"), "12.1": eolDate("2026-02-13"),
		"12.2": eolDate("2026-05-28"), "12.3": eolDate("2029-06-12"),
	}
	// MongoDB: the self-managed lifecycle table, a few years for a major and
	// until the next one for the minors between them. 8.3's row there says
	// both "TBD" and a date, so it is not listed until it says one.
	mongoEOL = map[string]time.Time{
		"3.6": eolDate("2021-04-30"), "4.0": eolDate("2022-04-30"), "4.2": eolDate("2023-04-30"),
		"4.4": eolDate("2024-02-29"), "5.0": eolDate("2024-10-31"), "6.0": eolDate("2025-07-31"),
		"7.0": eolDate("2027-08-31"), "8.0": eolDate("2029-10-31"), "8.1": eolDate("2025-09-30"),
		"8.2": eolDate("2026-07-31"), "9.0": eolDate("2031-10-31"),
	}
	// ClickHouse: a long-term release is maintained for a year from the day
	// it came out. Where that day is not listed the year is counted from the
	// start of the month the release is numbered after, which errs early.
	clickhouseEOL = map[string]time.Time{
		"25.3": eolDate("2026-03-20"), "25.8": eolDate("2026-08-29"),
		"26.3": eolDate("2027-03-26"), "26.8": eolDate("2027-08-27"),
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

	// Each engine's banner is read where its own version number stands, and
	// nowhere else: the first pair of numbers anywhere in a PostgreSQL banner
	// can be the compiler's ("18beta1 … compiled by gcc (Debian 12.2.0-14)").
	postgresVersion   = regexp.MustCompile(`^PostgreSQL (\d+)(?:\.(\d+))?`)
	mysqlVersion      = regexp.MustCompile(`^(?:5\.5\.5-)?(\d+)\.(\d+)`)
	clickhouseVersion = regexp.MustCompile(`^(?:ClickHouse )?(\d+)\.(\d+)`)
	mongoVersion      = regexp.MustCompile(`^(\d+)\.(\d+)`)
	mssqlYear         = regexp.MustCompile(`SQL Server (\d{4})`)
	oracleRelease     = regexp.MustCompile(`(?i)Database (\d+)[a-z]`)
)

// eolForks are the products that answer with another engine's banner and keep
// their own release calendar. Each carries the version of the engine it is
// compatible with — YugabyteDB says "PostgreSQL 11.2-YB-2.20", TiDB says
// "8.0.11-TiDB-v7.5.0" — and that number says nothing about whether the fork
// itself is maintained, so no finding is made for them.
var eolForks = []string{"-yb-", "yugabyte", "cockroach", "greenplum", "redshift", "tidb", "vitess", "oceanbase", "singlestore", "memsql"}

func eolFork(flavor, version string) bool {
	switch flavor {
	// FerretDB's banner is the MongoDB release it answers as, with nothing in
	// it to say so: only the flavour does.
	case FlavorCockroachDB, FlavorYugabyteDB, FlavorTiDB, FlavorFerretDB:
		return true
	}
	lower := strings.ToLower(version)
	for _, fork := range eolForks {
		if strings.Contains(lower, fork) {
			return true
		}
	}
	return false
}

// VersionEndOfLife looks a server's version string up in the table. The
// string is whatever the engine's own version query returned, and the flavour
// what the server was identified as; ok is false when the release is not one
// the table knows.
func VersionEndOfLife(driver Driver, flavor, version string, now time.Time) (*EndOfLife, bool) {
	if eolFork(flavor, version) {
		return nil, false
	}
	pattern := map[Driver]*regexp.Regexp{
		DriverPostgres: postgresVersion, DriverMySQL: mysqlVersion, DriverClickHouse: clickhouseVersion,
		DriverMongo: mongoVersion,
	}[driver]
	major, minor, found := leadingVersion(pattern, version)
	if pattern != nil && !found {
		return nil, false
	}
	numbered := strconv.Itoa(major) + "." + strconv.Itoa(minor)
	var product, release string
	switch driver {
	case DriverPostgres:
		// From 10 on the first number alone is the major; before it, two.
		product, release = "PostgreSQL", strconv.Itoa(major)
		if major < 10 {
			release = numbered
		}
	case DriverMySQL:
		product, release = "MySQL", numbered
		if strings.Contains(strings.ToLower(version), "mariadb") {
			product = "MariaDB"
		}
	case DriverMongo:
		product, release = "MongoDB", numbered
	case DriverClickHouse:
		product, release = "ClickHouse", numbered
	case DriverMSSQL:
		m := mssqlYear.FindStringSubmatch(version)
		if m == nil {
			return nil, false
		}
		product, release = "SQL Server", m[1]
	case DriverOracle:
		m := oracleRelease.FindStringSubmatch(version)
		if m == nil {
			return nil, false
		}
		product, release = "Oracle Database", m[1]
	default:
		return nil, false
	}
	return endOfLife(product, release, now)
}

// ReleaseEndOfLife looks a release line up by the product's flavour, for a
// release that is named rather than read from a server: the provisioning
// catalogue's "18" or "11.8".
func ReleaseEndOfLife(flavor, release string, now time.Time) (*EndOfLife, bool) {
	product := map[string]string{
		FlavorPostgres: "PostgreSQL", FlavorTimescaleDB: "PostgreSQL", FlavorMySQL: "MySQL",
		FlavorMariaDB: "MariaDB", FlavorMongoDB: "MongoDB", FlavorClickHouse: "ClickHouse",
		FlavorSQLServer: "SQL Server", FlavorOracle: "Oracle Database",
	}[flavor]
	if product == "" {
		return nil, false
	}
	return endOfLife(product, release, now)
}

func endOfLife(product, release string, now time.Time) (*EndOfLife, bool) {
	var (
		date time.Time
		ok   bool
	)
	switch product {
	case "PostgreSQL":
		date, ok = postgresEOL[release]
	case "MySQL":
		date, ok = mysqlEOL[release]
	case "MariaDB":
		date, ok = mariadbEOL[release]
	case "MongoDB":
		date, ok = mongoEOL[release]
	case "SQL Server":
		date, ok = mssqlEOL[release]
	case "Oracle Database":
		date, ok = oracleEOL[release]
	case "ClickHouse":
		date, ok = clickhouseEndOfLife(release)
	}
	if !ok {
		return nil, false
	}
	days := int(date.Sub(now).Hours() / 24)
	return &EndOfLife{Product: product, Release: release, Date: date, Past: now.After(date), DaysLeft: days}, true
}

// clickhouseEndOfLife reads a release the way ClickHouse numbers it, by the
// year and month it came out. Only the long-term releases, March's and
// August's, have a date: any other is maintained for as long as it is one of
// the three newest, which is a day nobody can publish in advance.
func clickhouseEndOfLife(release string) (time.Time, bool) {
	if date, ok := clickhouseEOL[release]; ok {
		return date, true
	}
	year, month, found := strings.Cut(release, ".")
	major, errYear := strconv.Atoi(year)
	minor, errMonth := strconv.Atoi(month)
	if !found || errYear != nil || errMonth != nil || major < 18 || (minor != 3 && minor != 8) {
		return time.Time{}, false
	}
	return time.Date(2000+major, time.Month(minor), 1, 0, 0, 0, 0, time.UTC).AddDate(1, 0, 0), true
}

// leadingVersion reads the major and minor an engine's banner begins with.
// The minor is zero where the banner has none — a PostgreSQL beta is "18beta1".
func leadingVersion(pattern *regexp.Regexp, version string) (major, minor int, ok bool) {
	if pattern == nil {
		return 0, 0, false
	}
	m := pattern.FindStringSubmatch(strings.TrimSpace(version))
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

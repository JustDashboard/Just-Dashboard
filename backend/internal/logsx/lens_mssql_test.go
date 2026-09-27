package logsx

import "testing"

// msAttrs is a map from pairs, for the want tables.
func msAttrs(pairs ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

// SQL Server's errorlog as the mssql/server:2022 image writes it to stdout:
// the research's logon failure verbatim, and the server's own wording for
// its start, backups, a full log and slow I/O.
var mssqlCases = []dbCase{
	{
		name: "the version banner and its indented lines, ready, recovery",
		lines: []string{
			"2024-03-18 10:15:23.45 Server      Microsoft SQL Server 2022 (RTM-CU12) (KB5033663) - 16.0.4115.5 (X64) ",
			"\tMar  4 2024 08:56:10 ",
			"\tCopyright (C) 2022 Microsoft Corporation",
			"\tDeveloper Edition (64-bit) on Linux (Ubuntu 22.04.4 LTS) <X64>",
			"2024-03-18 10:15:23.47 Server      UTC adjustment: 0:00",
			"2024-03-18 10:15:25.32 spid41s     SQL Server is now ready for client connections. This is an informational message; no user action is required.",
			"2016-11-21 14:21:52.30 spid5s      Recovery is complete. This is an informational message only. No user action is required.",
		},
		want: []dbWant{
			{at: "2024-03-18 10:15:23.45", event: "startup", attrs: msAttrs("component", "Server")},
			{cont: true},
			{cont: true},
			{cont: true},
			{at: "2024-03-18 10:15:23.47", attrs: msAttrs("component", "Server")},
			{at: "2024-03-18 10:15:25.32", event: "ready", attrs: msAttrs("component", "spid")},
			{at: "2016-11-21 14:21:52.3", event: "recovery", attrs: msAttrs("component", "spid")},
		},
	},
	{
		name: "a failed login is the second of two lines",
		lines: []string{
			"2016-11-21 14:23:20.65 Logon       Error: 18456, Severity: 14, State: 7.",
			"2016-11-21 14:23:20.65 Logon       Login failed for user 'sa'. Reason: An error occurred while evaluating the password. [CLIENT: 192.168.56.1]",
			"2024-01-01 00:00:00.12 Logon       Error: 18456, Severity: 14, State: 8.",
			"2024-01-01 00:00:00.12 Logon       Login failed for user 'sa'. Reason: Password did not match that for the login provided. [CLIENT: 172.17.0.1]",
		},
		want: []dbWant{
			{level: "warn", at: "2016-11-21 14:23:20.65", attrs: msAttrs("component", "Logon", "code", "18456")},
			{level: "warn", at: "2016-11-21 14:23:20.65", event: "auth_failed", attrs: msAttrs("component", "Logon",
				"code", "18456", "user", "sa", "client", "192.168.56.1")},
			{level: "warn", at: "2024-01-01 00:00:00.12", attrs: msAttrs("component", "Logon", "code", "18456")},
			{level: "warn", at: "2024-01-01 00:00:00.12", event: "auth_failed", attrs: msAttrs("component", "Logon",
				"code", "18456", "user", "sa", "client", "172.17.0.1")},
		},
	},
	{
		name: "backups, a full transaction log, slow I/O",
		lines: []string{
			"2024-03-18 10:20:00.11 Backup      Database backed up. Database: shop, creation date(time): 2024/03/18(10:15:25), pages dumped: 482, first LSN: 37:112:37, last LSN: 37:144:1, number of dump devices: 1, device information: (FILE=1, TYPE=DISK: {'/var/opt/mssql/backup/shop.bak'}). This is an informational message only. No user action is required.",
			"2024-03-18 10:20:00.12 Backup      BACKUP DATABASE successfully processed 474 pages in 0.050 seconds (74.023 MB/sec).",
			"2024-03-18 10:30:00.00 Backup      Error: 3041, Severity: 16, State: 1.",
			"2024-03-18 10:30:00.00 Backup      BACKUP failed to complete the command BACKUP DATABASE shop. Check the backup application log for detailed messages.",
			"2024-03-18 11:00:00.00 spid52      Error: 9002, Severity: 17, State: 2.",
			"2024-03-18 11:00:00.00 spid52      The transaction log for database 'shop' is full due to 'LOG_BACKUP'.",
			"2024-03-18 11:05:00.00 spid9s      SQL Server has encountered 1 occurrence(s) of I/O requests taking longer than 15 seconds to complete on file [/var/opt/mssql/data/shop.mdf] in database id 5.  The OS file handle is 0x0000000000000A2C.  The offset of the latest long I/O is: 0x0000000a3c0000.  The duration of the long I/O is: 15123 ms.",
		},
		want: []dbWant{
			{at: "2024-03-18 10:20:00.11", event: "backup", attrs: msAttrs("component", "Backup")},
			{at: "2024-03-18 10:20:00.12", event: "backup", attrs: msAttrs("component", "Backup")},
			{level: "error", at: "2024-03-18 10:30:00", attrs: msAttrs("component", "Backup", "code", "3041")},
			{level: "error", at: "2024-03-18 10:30:00", event: "backup", attrs: msAttrs("component", "Backup", "code", "3041")},
			{level: "error", at: "2024-03-18 11:00:00", attrs: msAttrs("component", "spid", "code", "9002")},
			{level: "error", at: "2024-03-18 11:00:00", event: "log_full", attrs: msAttrs("component", "spid", "code", "9002")},
			{at: "2024-03-18 11:05:00", event: "io_slow", attrs: msAttrs("component", "spid")},
		},
	},
	{
		name: "an error the lens has no name for, and one whose message never came",
		lines: []string{
			"2024-03-18 11:10:00.00 spid60      Error: 17053, Severity: 16, State: 1.",
			"2024-03-18 11:10:00.00 spid60      /var/opt/mssql/data/shop_log.ldf: Operating system error 112(There is not enough space on the disk.) encountered.",
			"2024-03-18 11:20:00.00 spid61      Error: 824, Severity: 24, State: 2.",
			"2024-03-18 11:20:01.00 spid70s     Starting up database 'tempdb'.",
		},
		want: []dbWant{
			{level: "error", at: "2024-03-18 11:10:00", attrs: msAttrs("component", "spid", "code", "17053")},
			{level: "error", at: "2024-03-18 11:10:00", event: "error", attrs: msAttrs("component", "spid", "code", "17053")},
			{level: "error", at: "2024-03-18 11:20:00", attrs: msAttrs("component", "spid", "code", "824")},
			// Another session, another second: not the message of the 824.
			{at: "2024-03-18 11:20:01", attrs: msAttrs("component", "spid")},
		},
	},
}

func TestMssqlLens(t *testing.T) {
	dbRun(t, "mssql", mssqlCases)
}

func BenchmarkLensMssql(b *testing.B) {
	var texts []string
	for _, c := range mssqlCases {
		texts = append(texts, c.lines...)
	}
	dbBench(b, "mssql", texts)
}

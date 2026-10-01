package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// The engine checks for SQLite, ClickHouse, SQL Server and Oracle.
//
// PostgreSQL and MySQL had theirs from the start; these four had only the two
// structure checks every engine shares, so their advisor said "nothing found"
// about servers nobody had actually looked at. Each check here reads one view
// and is on its own: a view the account may not read costs that one check and
// is named among what was not assessed, rather than failing the rest.

// --- SQLite --------------------------------------------------------------------

func (sqliteDialect) Advise(ctx context.Context, db *sql.DB, _ string) ([]Advice, []string, error) {
	f, err := ReadSQLiteFile(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	out := []Advice{}
	file := []AdviceTarget{{Kind: "database", Name: f.Path}}

	if !strings.EqualFold(f.JournalMode, "wal") && !strings.EqualFold(f.JournalMode, "memory") && f.Path != "" {
		out = append(out, Advice{
			ID: "journal-mode-not-wal", Level: "notice", Category: AdvicePerformance,
			Title:   "The database is not in WAL mode",
			Detail:  "In " + f.JournalMode + " mode a writer blocks every reader and every reader blocks the writer. An application with more than one connection waits on itself, and reports it as \"database is locked\".",
			Advice:  "Switch to write-ahead logging, which lets readers and one writer work at the same time. It needs every process that opens the file to be on this machine.",
			Targets: file, SQL: "PRAGMA journal_mode = WAL;", Link: "settings",
		})
	}
	// A fifth of the file, and enough bytes to be worth a rewrite.
	if f.PageCount > 0 && f.FreelistPages*5 > f.PageCount && f.ReclaimableBytes > 16*1024*1024 {
		out = append(out, Advice{
			ID: "free-pages", Level: "notice", Category: AdviceMaintenance,
			Title:   humanBytes(f.ReclaimableBytes) + " of the file is free pages",
			Detail:  "Deleted rows leave their pages in the file for reuse. They are not returned to the disk until the file is rebuilt.",
			Advice:  "Vacuum the database if the disk needs the space back. It rewrites the whole file, so it needs room for a second copy and nothing else can use the database while it runs.",
			Targets: file, SQL: "VACUUM;", Link: "performance",
		})
	}
	// A write-ahead log that has outgrown the database it belongs to: a
	// reader has been holding a snapshot open and no checkpoint could finish.
	if f.WALBytes > 64*1024*1024 && f.WALBytes > f.FileBytes/4 {
		out = append(out, Advice{
			ID: "wal-large", Level: "warning", Category: AdviceMaintenance,
			Title:   "The write-ahead log is " + humanBytes(f.WALBytes),
			Detail:  "The log is folded back into the database at each checkpoint, and a checkpoint cannot pass a reader that is still open. A log this large means one has been open for a long time, and every read now searches the whole of it.",
			Advice:  "Checkpoint the log, and find the connection that keeps a transaction open.",
			Targets: file, SQL: "PRAGMA wal_checkpoint(TRUNCATE);", Link: "performance",
		})
	}
	if f.Objects["table"] > 0 {
		var analysed int
		// sqlite_stat1 only exists once ANALYZE has run; asking for it when
		// it does not is an error, which is the answer.
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_stat1`).Scan(&analysed); err != nil || analysed == 0 {
			if f.PageCount*f.PageSize > 8*1024*1024 {
				out = append(out, Advice{
					ID: "never-analysed", Level: "notice", Category: AdviceMaintenance,
					Title:   "The query planner has no statistics",
					Detail:  "Without them SQLite picks between indexes by rule of thumb, which on a table with several is how a query ends up on the wrong one.",
					Advice:  "Run ANALYZE once, and PRAGMA optimize from the application when it closes a connection.",
					Targets: file, SQL: "ANALYZE;", Link: "performance",
				})
			}
		}
	}
	return out, nil, nil
}

// --- ClickHouse --------------------------------------------------------------

// The part count per partition at which ClickHouse starts delaying inserts
// by default is 1000 (parts_to_delay_insert) and refusing them 3000; a third
// of the way there is when it is worth knowing.
const clickhousePartsWarning = 300

func (d clickhouseDialect) Advise(ctx context.Context, db *sql.DB, schema string) ([]Advice, []string, error) {
	out := []Advice{}
	silences := []string{}
	silent := func(what string, err error) {
		silences = append(silences, what+" could not be assessed: "+err.Error())
	}
	rel := func(database, table string) string {
		q, err := qualify(d, database, table)
		if err != nil {
			return ""
		}
		return q
	}

	rows, err := db.QueryContext(ctx, `
	  SELECT database, table, partition, toInt64(count())
	  FROM system.parts
	  WHERE active AND database = ?
	  GROUP BY database, table, partition
	  HAVING count() > ?
	  ORDER BY count() DESC LIMIT 50`, schema, clickhousePartsWarning)
	if err != nil {
		silent("Part counts", err)
	} else {
		crowded, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var database, table, partition string
			var parts int64
			if err := rows.Scan(&database, &table, &partition, &parts); err != nil {
				rows.Close()
				return nil, nil, err
			}
			detail := fmt.Sprintf("%d parts in partition %s", parts, partition)
			crowded = append(crowded, table+" ("+detail+")")
			target := AdviceTarget{Kind: "table", Schema: database, Name: table, Detail: detail}
			if q := rel(database, table); q != "" {
				target.SQL = "OPTIMIZE TABLE " + q + ";"
			}
			targets = append(targets, target)
		}
		rows.Close()
		if len(crowded) > 0 {
			out = append(out, Advice{
				ID: "too-many-parts", Level: "warning", Category: AdviceReliability,
				Title:   fmt.Sprintf("%d partition%s with hundreds of parts", len(crowded), pluralS(len(crowded))),
				Detail:  "Every insert writes a part and merges fold them together afterwards. When inserts outrun the merges the server first delays inserts and then refuses them with \"Too many parts\".",
				Advice:  "Insert in larger batches, less often. Optimize schedules a merge now, which helps once and not the next time.",
				Objects: crowded, Targets: targets, SQL: targetSQL(targets), Link: "performance",
			})
		}
	}

	rows, err = db.QueryContext(ctx, `
	  SELECT database, table, mutation_id, substring(command, 1, 200), latest_fail_reason
	  FROM system.mutations
	  WHERE NOT is_done AND database = ?
	    AND (latest_fail_reason != '' OR create_time < now() - INTERVAL 1 HOUR)
	  ORDER BY create_time LIMIT 50`, schema)
	if err != nil {
		silent("Mutations", err)
	} else {
		stuck, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var database, table, id, command, reason string
			if err := rows.Scan(&database, &table, &id, &command, &reason); err != nil {
				rows.Close()
				return nil, nil, err
			}
			detail := command
			if reason != "" {
				detail += " — " + reason
			}
			stuck = append(stuck, table+": "+detail)
			targets = append(targets, AdviceTarget{Kind: "table", Schema: database, Name: table, Detail: detail,
				SQL: "KILL MUTATION WHERE database = " + dumpString(DriverClickHouse, database) +
					" AND table = " + dumpString(DriverClickHouse, table) +
					" AND mutation_id = " + dumpString(DriverClickHouse, id) + ";"})
		}
		rows.Close()
		if len(stuck) > 0 {
			out = append(out, Advice{
				ID: "stuck-mutation", Level: "warning", Category: AdviceReliability,
				Title:   fmt.Sprintf("%d mutation%s not finishing", len(stuck), pluralS(len(stuck))),
				Detail:  "A mutation rewrites every part it touches in the background and retries for ever when it fails. One that is stuck holds up every mutation queued behind it on the same table.",
				Advice:  "Read the failure reason; kill the mutation if it can never succeed, then run a corrected one.",
				Objects: stuck, Targets: targets, SQL: targetSQL(targets), Link: "performance",
			})
		}
	}

	rows, err = db.QueryContext(ctx, `
	  SELECT name FROM system.users
	  WHERE toString(auth_type) LIKE '%no_password%'
	  ORDER BY name`)
	if err != nil {
		silent("Accounts", err)
	} else {
		open, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				open = append(open, name)
				targets = append(targets, AdviceTarget{Kind: "role", Name: name})
			}
		}
		rows.Close()
		if len(open) > 0 {
			out = append(out, Advice{
				ID: "account-without-password", Level: "critical", Category: AdviceSecurity,
				Title:   fmt.Sprintf("%d account%s with no password", len(open), pluralS(len(open))),
				Detail:  "ClickHouse ships with a default account that needs none. Anyone who can reach the port is that account, with whatever it is allowed to do.",
				Advice:  "Give each one a password, or restrict the addresses it may connect from in the server's users configuration.",
				Objects: open, Targets: targets, Link: "access",
			})
		}
	}

	var readonly int64
	if err := db.QueryRowContext(ctx, `SELECT toInt64(count()) FROM system.replicas WHERE is_readonly`).Scan(&readonly); err != nil {
		silent("Replicas", err)
	} else if readonly > 0 {
		out = append(out, Advice{
			ID: "readonly-replica", Level: "critical", Category: AdviceReliability,
			Title:   fmt.Sprintf("%d replicated table%s read-only", readonly, pluralS(int(readonly))),
			Detail:  "A replica that has lost its session with ClickHouse Keeper refuses every insert until it has one again.",
			Advice:  "Check that Keeper is reachable from this server, then SYSTEM RESTART REPLICA for the tables that stay read-only.",
			Targets: []AdviceTarget{{Kind: "server", Name: "replication"}}, Link: "performance",
		})
	}

	rows, err = db.QueryContext(ctx, `
	  SELECT name, toInt64(free_space), toInt64(total_space) FROM system.disks
	  WHERE total_space > 0 AND free_space < total_space / 10
	  ORDER BY name`)
	if err != nil {
		silent("Disks", err)
	} else {
		full, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var name string
			var free, total int64
			if rows.Scan(&name, &free, &total) != nil {
				continue
			}
			detail := humanBytes(free) + " free of " + humanBytes(total)
			full = append(full, name+" ("+detail+")")
			targets = append(targets, AdviceTarget{Kind: "server", Name: "disk " + name, Detail: detail})
		}
		rows.Close()
		if len(full) > 0 {
			out = append(out, Advice{
				ID: "disk-nearly-full", Level: "critical", Category: AdviceReliability,
				Title:   fmt.Sprintf("%d disk%s under a tenth free", len(full), pluralS(len(full))),
				Detail:  "A merge writes its result before it removes its inputs, so merges are the first thing to stop on a full disk — and once they stop, parts pile up and inserts follow.",
				Advice:  "Free space or add a disk before the merges stop; a TTL on the largest tables is the lasting answer.",
				Objects: full, Targets: targets,
			})
		}
	}

	var detached int64
	if err := db.QueryRowContext(ctx, `SELECT toInt64(count()) FROM system.detached_parts WHERE database = ?`, schema).Scan(&detached); err == nil && detached > 0 {
		out = append(out, Advice{
			ID: "detached-parts", Level: "notice", Category: AdviceMaintenance,
			Title:   fmt.Sprintf("%d detached part%s on disk", detached, pluralS(int(detached))),
			Detail:  "Detached parts are data the server set aside — broken at start-up, or detached by hand — and they take disk space without belonging to any table.",
			Advice:  "Read system.detached_parts for the reason each was detached; attach the ones that are wanted and drop the rest.",
			Targets: []AdviceTarget{{Kind: "database", Name: schema}},
		})
	}
	return out, silences, nil
}

// --- SQL Server --------------------------------------------------------------

func (d mssqlDialect) Advise(ctx context.Context, db *sql.DB, _ string) ([]Advice, []string, error) {
	out := []Advice{}
	silences := []string{}

	var name string
	var autoShrink, autoClose bool
	var pageVerify string
	if err := db.QueryRowContext(ctx, `
	  SELECT name, is_auto_shrink_on, is_auto_close_on, page_verify_option_desc
	  FROM sys.databases WHERE database_id = DB_ID()`).Scan(&name, &autoShrink, &autoClose, &pageVerify); err != nil {
		silences = append(silences, "Database options could not be assessed: "+err.Error())
	} else if q, err := d.QuoteIdent(name); err == nil {
		database := []AdviceTarget{{Kind: "database", Name: name}}
		if autoShrink {
			out = append(out, Advice{
				ID: "auto-shrink-on", Level: "warning", Category: AdvicePerformance,
				Title:   "AUTO_SHRINK is on",
				Detail:  "The server periodically shrinks the files and the next insert grows them again. Each shrink fragments every index it moves.",
				Advice:  "Turn it off, and shrink by hand on the rare occasion a file has to give space back.",
				Targets: database, SQL: "ALTER DATABASE " + q + " SET AUTO_SHRINK OFF;",
			})
		}
		if autoClose {
			out = append(out, Advice{
				ID: "auto-close-on", Level: "warning", Category: AdvicePerformance,
				Title:   "AUTO_CLOSE is on",
				Detail:  "The database is shut down when its last connection closes and reopened by the next, which empties its cache each time. It is a setting for a desktop edition, not a server.",
				Advice:  "Turn it off.",
				Targets: database, SQL: "ALTER DATABASE " + q + " SET AUTO_CLOSE OFF;",
			})
		}
		if !strings.EqualFold(pageVerify, "CHECKSUM") {
			out = append(out, Advice{
				ID: "page-verify-not-checksum", Level: "warning", Category: AdviceReliability,
				Title:   "Pages are not verified by checksum",
				Detail:  "With page verification set to " + pageVerify + " a page the disk corrupted is read back as if it were sound; with CHECKSUM the server notices on the first read.",
				Advice:  "Set it to CHECKSUM. Existing pages gain one as they are next written.",
				Targets: database, SQL: "ALTER DATABASE " + q + " SET PAGE_VERIFY CHECKSUM;",
			})
		}
	}

	rows, err := db.QueryContext(ctx, `
	  SELECT name, is_disabled, is_policy_checked FROM sys.sql_logins
	  WHERE name NOT LIKE '##%' ORDER BY name`)
	if err != nil {
		silences = append(silences, "Logins could not be assessed: "+err.Error())
		return out, silences, nil
	}
	defer rows.Close()
	unchecked, targets := []string{}, []AdviceTarget{}
	for rows.Next() {
		var login string
		var disabled, checked bool
		if err := rows.Scan(&login, &disabled, &checked); err != nil {
			return nil, nil, err
		}
		q, qerr := d.QuoteIdent(login)
		if login == "sa" && !disabled && qerr == nil {
			out = append(out, Advice{
				ID: "sa-enabled", Level: "warning", Category: AdviceSecurity,
				Title:   "The sa login is enabled",
				Detail:  "sa is the one account name every attacker already knows, and it cannot be locked out by failed attempts.",
				Advice:  "Administer through a named login in the sysadmin role and disable sa.",
				Targets: []AdviceTarget{{Kind: "role", Name: "sa"}}, SQL: "ALTER LOGIN " + q + " DISABLE;", Link: "access",
			})
		}
		if !checked && !disabled && qerr == nil {
			unchecked = append(unchecked, login)
			targets = append(targets, AdviceTarget{Kind: "role", Name: login, SQL: "ALTER LOGIN " + q + " WITH CHECK_POLICY = ON;"})
		}
	}
	if len(unchecked) > 0 {
		out = append(out, Advice{
			ID: "login-without-password-policy", Level: "notice", Category: AdviceSecurity,
			Title:   fmt.Sprintf("%d login%s exempt from the password policy", len(unchecked), pluralS(len(unchecked))),
			Detail:  "A login with the policy off accepts any password and is never locked out after failed attempts.",
			Advice:  "Turn the policy on for each.",
			Objects: unchecked, Targets: targets, SQL: targetSQL(targets), Link: "access",
		})
	}
	return out, silences, rows.Err()
}

// --- Oracle ------------------------------------------------------------------

func (d oracleDialect) Advise(ctx context.Context, db *sql.DB, schema string) ([]Advice, []string, error) {
	out := []Advice{}
	silences := []string{}
	// Oracle has no empty string: an owner of '' is NULL and matches nothing,
	// so "no schema named" is bound as a real NULL and the query falls back
	// to the session's own.
	var owner any
	if strings.TrimSpace(schema) != "" {
		owner = schema
	}

	// Objects that no longer compile: a procedure whose table changed under
	// it fails only when it is next called.
	rows, err := db.QueryContext(ctx, `
	  SELECT owner, object_type, object_name FROM all_objects
	  WHERE status = 'INVALID' AND owner = NVL(:1, SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA'))
	    AND ROWNUM <= 50
	  ORDER BY object_type, object_name`, owner)
	if err != nil {
		silences = append(silences, "Invalid objects could not be assessed: "+err.Error())
	} else {
		invalid, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var owner, kind, name string
			if err := rows.Scan(&owner, &kind, &name); err != nil {
				rows.Close()
				return nil, nil, err
			}
			invalid = append(invalid, strings.ToLower(kind)+" "+name)
			target := AdviceTarget{Kind: "object", Schema: owner, Name: name, Detail: strings.ToLower(kind)}
			if stmt, ok := oracleCompileSQL(d, owner, kind, name); ok {
				target.SQL = stmt
			}
			targets = append(targets, target)
		}
		rows.Close()
		if len(invalid) > 0 {
			out = append(out, Advice{
				ID: "invalid-objects", Level: "warning", Category: AdviceReliability,
				Title:   fmt.Sprintf("%d invalid object%s", len(invalid), pluralS(len(invalid))),
				Detail:  "An object goes invalid when something it depends on changes. It is recompiled on its next use, and if that fails the caller gets the error.",
				Advice:  "Compile each one now and read what it reports, rather than finding out from the application.",
				Objects: invalid, Targets: targets, SQL: targetSQL(targets),
			})
		}
	}

	rows, err = db.QueryContext(ctx, `
	  SELECT owner, table_name FROM all_tables
	  WHERE owner = NVL(:1, SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA'))
	    AND last_analyzed IS NULL AND temporary = 'N' AND ROWNUM <= 50
	  ORDER BY table_name`, owner)
	if err != nil {
		silences = append(silences, "Optimizer statistics could not be assessed: "+err.Error())
	} else {
		never, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var owner, table string
			if err := rows.Scan(&owner, &table); err != nil {
				rows.Close()
				return nil, nil, err
			}
			never = append(never, table)
			targets = append(targets, AdviceTarget{Kind: "table", Schema: owner, Name: table,
				SQL: "BEGIN DBMS_STATS.GATHER_TABLE_STATS(" + dumpString(DriverOracle, owner) + ", " + dumpString(DriverOracle, table) + "); END;"})
		}
		rows.Close()
		if len(never) > 0 {
			out = append(out, Advice{
				ID: "never-analysed", Level: "notice", Category: AdviceMaintenance,
				Title:   fmt.Sprintf("%d table%s with no optimizer statistics", len(never), pluralS(len(never))),
				Detail:  "Without statistics the optimizer samples the table each time it plans a query against it, and plans on what a sample happens to show.",
				Advice:  "Gather statistics once; the nightly maintenance job keeps them current afterwards.",
				Objects: never, Targets: targets, SQL: targetSQL(targets),
			})
		}
	}

	// The two below read DBA views. An application schema cannot, and that
	// is said rather than reported as a clean result.
	rows, err = db.QueryContext(ctx, `SELECT username FROM dba_users_with_defpwd ORDER BY username`)
	if err != nil {
		silences = append(silences, "Default passwords could not be assessed without the DBA views.")
	} else {
		defaults, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				defaults = append(defaults, name)
				targets = append(targets, AdviceTarget{Kind: "role", Name: name})
			}
		}
		rows.Close()
		if len(defaults) > 0 {
			out = append(out, Advice{
				ID: "default-passwords", Level: "critical", Category: AdviceSecurity,
				Title:   fmt.Sprintf("%d account%s still on the default password", len(defaults), pluralS(len(defaults))),
				Detail:  "These accounts were installed with a password that is printed in the documentation.",
				Advice:  "Change each password, or lock the accounts nothing uses.",
				Objects: defaults, Targets: targets,
			})
		}
	}
	rows, err = db.QueryContext(ctx, `
	  SELECT tablespace_name, ROUND(used_percent, 1) FROM dba_tablespace_usage_metrics
	  WHERE used_percent > 90 ORDER BY used_percent DESC`)
	if err != nil {
		silences = append(silences, "Tablespace usage could not be assessed without the DBA views.")
	} else {
		full, targets := []string{}, []AdviceTarget{}
		for rows.Next() {
			var name string
			var used float64
			if rows.Scan(&name, &used) == nil {
				detail := fmt.Sprintf("%.1f%% used", used)
				full = append(full, name+" ("+detail+")")
				targets = append(targets, AdviceTarget{Kind: "server", Name: "tablespace " + name, Detail: detail})
			}
		}
		rows.Close()
		if len(full) > 0 {
			out = append(out, Advice{
				ID: "tablespace-nearly-full", Level: "critical", Category: AdviceReliability,
				Title:   fmt.Sprintf("%d tablespace%s over 90%% full", len(full), pluralS(len(full))),
				Detail:  "When a tablespace cannot extend, every insert into a segment in it fails with ORA-01653.",
				Advice:  "Add a datafile or let the existing one autoextend.",
				Objects: full, Targets: targets,
			})
		}
	}
	return out, silences, nil
}

// oracleCompileSQL renders the statement that recompiles one invalid object.
// A package body compiles through its package, and the kinds that are not
// code have no COMPILE at all.
func oracleCompileSQL(d oracleDialect, owner, kind, name string) (string, bool) {
	o, err := d.QuoteIdent(owner)
	if err != nil {
		return "", false
	}
	n, err := d.QuoteIdent(name)
	if err != nil {
		return "", false
	}
	switch kind {
	case "PROCEDURE", "FUNCTION", "TRIGGER", "VIEW", "PACKAGE", "TYPE":
		return "ALTER " + kind + " " + o + "." + n + " COMPILE;", true
	case "PACKAGE BODY":
		return "ALTER PACKAGE " + o + "." + n + " COMPILE BODY;", true
	case "TYPE BODY":
		return "ALTER TYPE " + o + "." + n + " COMPILE BODY;", true
	case "MATERIALIZED VIEW":
		return "ALTER MATERIALIZED VIEW " + o + "." + n + " COMPILE;", true
	}
	return "", false
}

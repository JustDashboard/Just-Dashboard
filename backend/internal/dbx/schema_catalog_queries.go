package dbx

// Templates bind the schema and selected table names. They deliberately use the
// same catalogues, type spelling, constraint order and referential actions as
// each dialect's table reads. No user text is interpolated into SQL.
const standardSchemaColumns = `SELECT table_name, column_name, data_type, is_nullable,
 character_maximum_length, numeric_precision, numeric_scale
 FROM information_schema.columns
 WHERE table_schema = {schema} AND table_name IN ({tables})
 ORDER BY table_name, ordinal_position`

const standardSchemaPrimary = `SELECT tc.table_name, kcu.column_name
 FROM information_schema.table_constraints tc
 JOIN information_schema.key_column_usage kcu
 ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema
 AND kcu.table_name = tc.table_name
 WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_schema = {schema}
 AND tc.table_name IN ({tables}) ORDER BY tc.table_name, kcu.ordinal_position`

// Postgres reads pg_catalog rather than information_schema for three reasons
// the standard views cannot be argued out of: they have no row for a
// materialized view's columns, they spell every array as ARRAY and every enum
// as USER-DEFINED, and they hide a constraint from a login that holds only
// SELECT on its table — so a read-only account saw a schema with no keys.
func (postgresDialect) schemaQueries() schemaQueries {
	return schemaQueries{
		columns: `SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod),
 CASE WHEN a.attnotnull THEN 'NO' ELSE 'YES' END, NULL::int, NULL::int, NULL::int
 FROM pg_attribute a
 JOIN pg_class c ON c.oid = a.attrelid
 JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = {schema} AND c.relname IN ({tables}) AND a.attnum > 0 AND NOT a.attisdropped
 ORDER BY c.relname, a.attnum`,
		primary: `SELECT t.relname, a.attname
 FROM pg_index ix
 JOIN pg_class t ON t.oid = ix.indrelid
 JOIN pg_namespace n ON n.oid = t.relnamespace
 JOIN unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
 JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
 WHERE ix.indisprimary AND n.nspname = {schema} AND t.relname IN ({tables})
 ORDER BY t.relname, k.ord`,
		// Key columns only, so an INCLUDE column is not mistaken for part of
		// the key; an expression is reported as its text, so an index on
		// (lower(email), id) is not mistaken for one on (id); and a partial
		// index is not reported unique, because it is not.
		indexes: `SELECT t.relname, i.relname,
 COALESCE(a.attname, pg_get_indexdef(ix.indexrelid, k.ord::int, true)),
 ix.indisunique AND ix.indpred IS NULL
 FROM pg_class t
 JOIN pg_namespace n ON n.oid = t.relnamespace
 JOIN pg_index ix ON t.oid = ix.indrelid
 JOIN pg_class i ON i.oid = ix.indexrelid
 JOIN unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord) ON k.ord <= ix.indnkeyatts
 LEFT JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum AND k.attnum > 0
 WHERE n.nspname = {schema} AND t.relname IN ({tables})
 ORDER BY t.relname, i.relname, k.ord`,
		foreign: `SELECT rel.relname, con.conname, att.attname, nsp.nspname, cl.relname, att2.attname,
 con.confupdtype, con.confdeltype
 FROM pg_constraint con
 JOIN pg_class rel ON rel.oid = con.conrelid
 JOIN pg_namespace rn ON rn.oid = rel.relnamespace
 JOIN pg_class cl ON cl.oid = con.confrelid
 JOIN pg_namespace nsp ON nsp.oid = cl.relnamespace
 JOIN unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord) ON true
 JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = k.attnum
 JOIN unnest(con.confkey) WITH ORDINALITY AS fk(attnum, ord) ON fk.ord = k.ord
 JOIN pg_attribute att2 ON att2.attrelid = con.confrelid AND att2.attnum = fk.attnum
 WHERE con.contype = 'f' AND rn.nspname = {schema} AND rel.relname IN ({tables})
 ORDER BY rel.relname, con.conname, k.ord`,
		action: pgFKAction,
	}
}

func (mysqlDialect) schemaQueries() schemaQueries {
	return schemaQueries{
		columns: `SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, NULL, NULL, NULL
 FROM information_schema.COLUMNS
 WHERE TABLE_SCHEMA = {schema} AND TABLE_NAME IN ({tables}) ORDER BY TABLE_NAME, ORDINAL_POSITION`,
		primary: standardSchemaPrimary,
		indexes: `SELECT TABLE_NAME, INDEX_NAME, COLUMN_NAME, NON_UNIQUE = 0
 FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = {schema} AND TABLE_NAME IN ({tables})
 ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`,
		foreign: `SELECT k.TABLE_NAME, k.CONSTRAINT_NAME, k.COLUMN_NAME, k.REFERENCED_TABLE_SCHEMA,
 k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME, COALESCE(r.UPDATE_RULE, ''), COALESCE(r.DELETE_RULE, '')
 FROM information_schema.KEY_COLUMN_USAGE k
 LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS r
 ON r.CONSTRAINT_NAME = k.CONSTRAINT_NAME AND r.CONSTRAINT_SCHEMA = k.TABLE_SCHEMA
 WHERE k.TABLE_SCHEMA = {schema} AND k.TABLE_NAME IN ({tables}) AND k.REFERENCED_TABLE_NAME IS NOT NULL
 ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`,
	}
}

func (mssqlDialect) schemaQueries() schemaQueries {
	return schemaQueries{
		columns: standardSchemaColumns,
		primary: standardSchemaPrimary,
		// Included columns are stored in the index and are not part of its key,
		// and a filtered index is unique only among the rows it covers.
		indexes: `SELECT o.name, i.name, c.name,
 CAST(CASE WHEN i.is_unique = 1 AND i.has_filter = 0 THEN 1 ELSE 0 END AS BIT)
 FROM sys.indexes i
 JOIN sys.objects o ON o.object_id = i.object_id
 JOIN sys.schemas s ON s.schema_id = o.schema_id
 JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
 JOIN sys.columns c ON c.object_id = i.object_id AND c.column_id = ic.column_id
 WHERE s.name = {schema} AND o.name IN ({tables}) AND i.name IS NOT NULL
 AND ic.is_included_column = 0
 ORDER BY o.name, i.name, ic.key_ordinal`,
		foreign: `SELECT pt.name, fk.name, pc.name, rs.name, rt.name, rc.name,
 fk.update_referential_action_desc, fk.delete_referential_action_desc
 FROM sys.foreign_keys fk
 JOIN sys.tables pt ON pt.object_id = fk.parent_object_id
 JOIN sys.schemas ps ON ps.schema_id = pt.schema_id
 JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
 JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
 JOIN sys.tables rt ON rt.object_id = fk.referenced_object_id
 JOIN sys.schemas rs ON rs.schema_id = rt.schema_id
 JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
 WHERE ps.name = {schema} AND pt.name IN ({tables}) ORDER BY pt.name, fk.name, fkc.constraint_column_id`,
		action: underscoreToWords,
	}
}

func (oracleDialect) schemaQueries() schemaQueries {
	return schemaQueries{
		columns: `SELECT table_name, column_name,
 data_type || CASE WHEN data_type IN ('VARCHAR2','NVARCHAR2','CHAR','NCHAR') THEN '(' || char_length || ')'
 WHEN data_type = 'RAW' THEN '(' || data_length || ')'
 WHEN data_type = 'NUMBER' AND data_precision IS NOT NULL
 THEN '(' || data_precision || ',' || NVL(data_scale,0) || ')' ELSE '' END,
 nullable, NULL, NULL, NULL
 FROM all_tab_columns WHERE owner = {schema} AND table_name IN ({tables}) ORDER BY table_name, column_id`,
		primary: `SELECT c.table_name, cc.column_name FROM all_constraints c
 JOIN all_cons_columns cc ON cc.constraint_name = c.constraint_name AND cc.owner = c.owner
 WHERE c.constraint_type = 'P' AND c.owner = {schema} AND c.table_name IN ({tables})
 ORDER BY c.table_name, cc.position`,
		indexes: `SELECT i.table_name, i.index_name, ic.column_name,
 CASE WHEN i.uniqueness = 'UNIQUE' THEN 1 ELSE 0 END
 FROM all_indexes i JOIN all_ind_columns ic ON ic.index_name = i.index_name AND ic.index_owner = i.owner
 WHERE i.table_owner = {schema} AND i.table_name IN ({tables})
 ORDER BY i.table_name, i.index_name, ic.column_position`,
		foreign: `SELECT c.table_name, c.constraint_name, cc.column_name, rc.owner, rc.table_name,
 rcc.column_name, NULL, c.delete_rule
 FROM all_constraints c
 JOIN all_cons_columns cc ON cc.constraint_name = c.constraint_name AND cc.owner = c.owner
 JOIN all_constraints rc ON rc.constraint_name = c.r_constraint_name AND rc.owner = c.r_owner
 JOIN all_cons_columns rcc ON rcc.constraint_name = rc.constraint_name AND rcc.owner = rc.owner
 AND rcc.position = cc.position
 WHERE c.constraint_type = 'R' AND c.owner = {schema} AND c.table_name IN ({tables})
 ORDER BY c.table_name, c.constraint_name, cc.position`,
	}
}

func (sqliteDialect) schemaQueries() schemaQueries {
	return schemaQueries{
		nullIndexColumns: true,
		columns: `SELECT m.name, p.name, p.type, CASE p."notnull" WHEN 0 THEN 'YES' ELSE 'NO' END, NULL, NULL, NULL
 FROM sqlite_master m JOIN pragma_table_xinfo(m.name) p
 WHERE {schema} = 'main' AND m.name IN ({tables}) AND p.hidden <> 1 ORDER BY m.name, p.cid`,
		primary: `SELECT m.name, p.name FROM sqlite_master m JOIN pragma_table_info(m.name) p
 WHERE {schema} = 'main' AND m.name IN ({tables}) AND p.pk > 0 ORDER BY m.name, p.pk`,
		// A partial index is unique only among the rows it covers.
		indexes: `SELECT m.name, i.name, p.name, i."unique" AND NOT i.partial
 FROM sqlite_master m JOIN pragma_index_list(m.name) i LEFT JOIN pragma_index_info(i.name) p ON true
 WHERE {schema} = 'main' AND m.name IN ({tables}) ORDER BY m.name, i.seq, p.seqno`,
		foreign: `SELECT m.name, 'fk_' || p.id, p."from", '', p."table", p."to", p.on_update, p.on_delete
 FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) p
 WHERE {schema} = 'main' AND m.name IN ({tables}) ORDER BY m.name, p.id, p.seq`,
	}
}

func (clickhouseDialect) schemaQueries() schemaQueries {
	return schemaQueries{
		columns: `SELECT table, name, type, if(startsWith(type, 'Nullable('), 'YES', 'NO'), NULL, NULL, NULL
 FROM system.columns WHERE database = {schema} AND table IN ({tables}) ORDER BY table, position`,
		primary: `SELECT table, name FROM system.columns
 WHERE database = {schema} AND table IN ({tables}) AND is_in_primary_key = 1 ORDER BY table, position`,
		// Neither sorting keys nor skipping indexes promise uniqueness, and
		// ClickHouse has no foreign keys. The graph needs no other index facts.
	}
}

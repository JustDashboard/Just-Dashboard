package dbx

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// MissingIndex is one entry of SQL Server's own list of indexes its plans
// would have used, with the CREATE INDEX that list implies.
type MissingIndex struct {
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Equality   []string `json:"equality"`
	Inequality []string `json:"inequality"`
	Include    []string `json:"include"`
	// Seeks and Scans are the compiled plans that would have used the index.
	Seeks int64 `json:"seeks"`
	Scans int64 `json:"scans"`
	// AvgCost is the average cost of those queries in the optimizer's units,
	// and AvgImpact the percentage the optimizer expects the index to save.
	AvgCost   float64    `json:"avgCost"`
	AvgImpact float64    `json:"avgImpact"`
	LastSeek  *time.Time `json:"lastSeek,omitempty"`
	Statement string     `json:"statement"`
}

// MissingIndexesReport is the list, or the reason there is none to show.
type MissingIndexesReport struct {
	Supported bool           `json:"supported"`
	Reason    string         `json:"reason,omitempty"`
	Indexes   []MissingIndex `json:"indexes"`
	// Since is when the server last started: the views are emptied by a
	// restart, so a short list may be a short memory.
	Since *time.Time `json:"since,omitempty"`
}

// missingIndexer is implemented by the dialect whose engine keeps such a list.
type missingIndexer interface {
	MissingIndexes(ctx context.Context, db *sql.DB, limit int) (*MissingIndexesReport, error)
}

// MissingIndexesLimit is how many entries one reading carries, most valuable first.
const MissingIndexesLimit = 50

// ListMissingIndexes reads the engine's missing-index views.
func ListMissingIndexes(ctx context.Context, db *sql.DB, driver Driver) (*MissingIndexesReport, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	m, ok := d.(missingIndexer)
	if !ok {
		return &MissingIndexesReport{Indexes: []MissingIndex{}, Reason: "this engine keeps no list of missing indexes"}, nil
	}
	out, err := m.MissingIndexes(ctx, db, MissingIndexesLimit)
	if err != nil {
		return nil, err
	}
	if out.Indexes == nil {
		out.Indexes = []MissingIndex{}
	}
	return out, nil
}

// MissingIndexes reads sys.dm_db_missing_index_*: what the optimizer wished
// for while it compiled plans in this database, ranked by what it expects the
// index to be worth (cost x impact x the plans that wanted it). The views need
// VIEW SERVER STATE, and a login without it is told so.
func (d mssqlDialect) MissingIndexes(ctx context.Context, db *sql.DB, limit int) (*MissingIndexesReport, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT TOP (@p1)
	         OBJECT_SCHEMA_NAME(d.object_id, d.database_id), OBJECT_NAME(d.object_id, d.database_id),
	         ISNULL(d.equality_columns, ''), ISNULL(d.inequality_columns, ''), ISNULL(d.included_columns, ''),
	         s.user_seeks, s.user_scans, s.avg_total_user_cost, s.avg_user_impact, s.last_user_seek
	  FROM sys.dm_db_missing_index_groups g
	  JOIN sys.dm_db_missing_index_group_stats s ON s.group_handle = g.index_group_handle
	  JOIN sys.dm_db_missing_index_details d ON d.index_handle = g.index_handle
	  WHERE d.database_id = DB_ID() AND OBJECT_NAME(d.object_id, d.database_id) IS NOT NULL
	  ORDER BY s.avg_total_user_cost * s.avg_user_impact * (s.user_seeks + s.user_scans) DESC`, limit)
	if mssqlRefused(err) {
		return &MissingIndexesReport{Indexes: []MissingIndex{},
			Reason: "This login may not read the missing-index views (they need VIEW SERVER STATE): " + err.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &MissingIndexesReport{Supported: true, Indexes: []MissingIndex{}}
	for rows.Next() {
		var m MissingIndex
		var equality, inequality, include string
		var last sql.NullTime
		if err := rows.Scan(&m.Schema, &m.Table, &equality, &inequality, &include,
			&m.Seeks, &m.Scans, &m.AvgCost, &m.AvgImpact, &last); err != nil {
			return nil, err
		}
		// A list that does not read is skipped, not guessed at: the statement
		// below is only ever made from names read in full.
		var ok [3]bool
		m.Equality, ok[0] = parseBracketList(equality)
		m.Inequality, ok[1] = parseBracketList(inequality)
		m.Include, ok[2] = parseBracketList(include)
		if !ok[0] || !ok[1] || !ok[2] {
			continue
		}
		if last.Valid {
			t := last.Time.UTC()
			m.LastSeek = &t
		}
		if m.Statement, err = missingIndexStatement(d, m); err != nil {
			continue
		}
		out.Indexes = append(out.Indexes, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var started sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT sqlserver_start_time FROM sys.dm_os_sys_info`).Scan(&started); err == nil && started.Valid {
		t := started.Time.UTC()
		out.Since = &t
	}
	return out, nil
}

// missingIndexStatement writes the CREATE INDEX an entry implies: the equality
// columns lead, the inequality ones follow, and the rest are included.
func missingIndexStatement(d Dialect, m MissingIndex) (string, error) {
	keys := append(append([]string{}, m.Equality...), m.Inequality...)
	if len(keys) == 0 {
		return "", errNoKeyColumns
	}
	rel, err := qualify(d, m.Schema, m.Table)
	if err != nil {
		return "", err
	}
	name, err := d.QuoteIdent(generatedName(m.Table, keys, "idx"))
	if err != nil {
		return "", err
	}
	key, err := quoteColumns(d, "an index", keys)
	if err != nil {
		return "", err
	}
	statement := "CREATE NONCLUSTERED INDEX " + name + " ON " + rel + " (" + key + ")"
	if len(m.Include) > 0 {
		include, err := quoteColumns(d, "an index", m.Include)
		if err != nil {
			return "", err
		}
		statement += " INCLUDE (" + include + ")"
	}
	return statement, nil
}

var errNoKeyColumns = errors.New("the entry names no column to key the index on")

// parseBracketList reads SQL Server's column lists, `[a], [b]`, in which a
// name may hold a comma, a space or a `]` written `]]`. It reports false for
// text that is not such a list, and an empty list for empty text.
func parseBracketList(s string) ([]string, bool) {
	names := []string{}
	s = strings.TrimSpace(s)
	for s != "" {
		if s[0] != '[' {
			return nil, false
		}
		var name strings.Builder
		i := 1
		closed := false
		for i < len(s) {
			if s[i] == ']' {
				if i+1 < len(s) && s[i+1] == ']' {
					name.WriteByte(']')
					i += 2
					continue
				}
				i++
				closed = true
				break
			}
			name.WriteByte(s[i])
			i++
		}
		if !closed {
			return nil, false
		}
		names = append(names, name.String())
		s = strings.TrimSpace(s[i:])
		if s == "" {
			break
		}
		if s[0] != ',' {
			return nil, false
		}
		s = strings.TrimSpace(s[1:])
		if s == "" {
			return nil, false
		}
	}
	return names, true
}

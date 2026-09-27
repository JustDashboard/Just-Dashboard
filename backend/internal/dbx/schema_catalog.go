package dbx

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// These reads contain only the facts used by completion and the diagram. Full
// table details and mutation preconditions continue through the ordinary dialect.
type schemaQueries struct {
	columns, primary, indexes, foreign string
	action                             func(string) string
	nullIndexColumns                   bool
}

type catalogTable struct{ schema, name string }
type schemaCatalog struct {
	dialect Dialect
	columns map[catalogTable][]Column
	primary map[catalogTable][]string
	indexes map[catalogTable][]Index
	foreign map[catalogTable][]ForeignKey
}

func (c *schemaCatalog) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	if value, ok := c.columns[catalogTable{schema, table}]; ok {
		return value, nil
	}
	return c.dialect.Columns(ctx, db, schema, table)
}

func (c *schemaCatalog) PrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	if value, ok := c.primary[catalogTable{schema, table}]; ok {
		return value, nil
	}
	return c.dialect.PrimaryKey(ctx, db, schema, table)
}

func (c *schemaCatalog) Indexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	if value, ok := c.indexes[catalogTable{schema, table}]; ok {
		return value, nil
	}
	return c.dialect.Indexes(ctx, db, schema, table)
}

func (c *schemaCatalog) ForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error) {
	if value, ok := c.foreign[catalogTable{schema, table}]; ok {
		return value, nil
	}
	return c.dialect.ForeignKeys(ctx, db, schema, table)
}

func withSchemaCatalog(ctx context.Context, db *sql.DB, d Dialect, tables []Table, graph bool) *schemaCatalog {
	c := &schemaCatalog{dialect: d, columns: map[catalogTable][]Column{}, primary: map[catalogTable][]string{},
		indexes: map[catalogTable][]Index{}, foreign: map[catalogTable][]ForeignKey{}}
	groups := map[string][]Table{}
	for _, table := range tables {
		groups[table.Schema] = append(groups[table.Schema], table)
	}
	queries := d.schemaQueries()
	for schema, group := range groups {
		// Stay below every supported engine's bind/IN limit, including SQLite
		// builds with 999 variables and Oracle's 1,000-element IN limit.
		for start := 0; start < len(group); start += 500 {
			batch := group[start:min(start+500, len(group))]
			args := []any{schema}
			markers := make([]string, len(batch))
			for i, table := range batch {
				args = append(args, table.Name)
				markers[i] = d.Placeholder(i + 2)
			}
			read := func(query string, scan func(*sql.Rows) error) error {
				if query == "" {
					return nil
				}
				query = strings.ReplaceAll(query, "{schema}", d.Placeholder(1))
				query = strings.ReplaceAll(query, "{tables}", strings.Join(markers, ","))
				rows, err := db.QueryContext(ctx, query, args...)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					if err := scan(rows); err != nil {
						return err
					}
				}
				return rows.Err()
			}
			columns := map[string][]Column{}
			if err := read(queries.columns, func(rows *sql.Rows) error {
				var table, nullable string
				var column Column
				var length, precision, scale sql.NullInt64
				if err := rows.Scan(&table, &column.Name, &column.Type, &nullable, &length, &precision, &scale); err != nil {
					return err
				}
				column.Nullable = nullable == "YES" || nullable == "Y" || nullable == "1"
				column.Type = withTypeSize(column.Type, length, precision, scale)
				columns[table] = append(columns[table], column)
				return nil
			}); err == nil {
				for _, table := range batch {
					c.columns[catalogTable{schema, table.Name}] = columns[table.Name]
				}
			}
			if !graph {
				continue
			}
			primary := map[string][]string{}
			if err := read(queries.primary, func(rows *sql.Rows) error {
				var table, column string
				if err := rows.Scan(&table, &column); err != nil {
					return err
				}
				primary[table] = append(primary[table], column)
				return nil
			}); err == nil {
				for _, table := range batch {
					c.primary[catalogTable{schema, table.Name}] = primary[table.Name]
				}
			}
			indexes := map[string]*indexAcc{}
			if err := read(queries.indexes, func(rows *sql.Rows) error {
				var table, name string
				var column sql.NullString
				var unique bool
				if err := rows.Scan(&table, &name, &column, &unique); err != nil {
					return err
				}
				if !column.Valid && !queries.nullIndexColumns {
					return errors.New("catalogue index has no column name")
				}
				if indexes[table] == nil {
					indexes[table] = newIndexAcc()
				}
				if queries.nullIndexColumns {
					indexes[table].add(name, "", unique, false)
					if column.Valid {
						indexes[table].byKey[name].Columns = append(indexes[table].byKey[name].Columns, column.String)
					}
				} else {
					indexes[table].add(name, column.String, unique, false)
				}
				return nil
			}); err == nil {
				for _, table := range batch {
					var value []Index
					if acc := indexes[table.Name]; acc != nil {
						value = acc.slice()
					}
					c.indexes[catalogTable{schema, table.Name}] = value
				}
			}
			foreign := map[string]*fkAcc{}
			if err := read(queries.foreign, func(rows *sql.Rows) error {
				var table, name, column, refSchema, refTable, refColumn, update, del string
				if err := rows.Scan(&table, &name, &column, nullText{&refSchema}, &refTable, nullText{&refColumn}, nullText{&update}, nullText{&del}); err != nil {
					return err
				}
				if foreign[table] == nil {
					foreign[table] = newFKAcc()
				}
				fk := foreign[table].get(name)
				fk.Columns, fk.RefColumns = append(fk.Columns, column), append(fk.RefColumns, refColumn)
				fk.RefSchema, fk.RefTable = refSchema, refTable
				if queries.action != nil {
					update, del = queries.action(update), queries.action(del)
				}
				fk.OnUpdate, fk.OnDelete = update, del
				return nil
			}); err == nil {
				for _, table := range batch {
					var value []ForeignKey
					if acc := foreign[table.Name]; acc != nil {
						value = acc.slice()
					}
					c.foreign[catalogTable{schema, table.Name}] = value
				}
			}
		}
	}
	// A refused bulk catalogue falls back per table, retaining the old partial
	// answer when a login can inspect only some of the schema.
	return c
}

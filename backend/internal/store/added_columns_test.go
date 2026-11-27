package store

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// addedColumnsFile is every (table, column) pair addedColumns has held. The
// list in store.go says never to remove one, and a sentence is all that said
// it: an entry deleted in a tidy-up fails nothing, boots every new install
// fine, and leaves each install that stopped before that column without it.
const addedColumnsFile = "testdata/added_columns.txt"

func readAddedColumnsFile(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(addedColumnsFile)
	if err != nil {
		t.Fatal(err)
	}
	var pairs []string
	for line := range strings.Lines(string(raw)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(strings.Fields(line)) != 2 {
			t.Fatalf("%s: %q is not a table and a column", addedColumnsFile, line)
		}
		pairs = append(pairs, line)
	}
	return pairs
}

func TestNoShippedAddedColumnIsRemoved(t *testing.T) {
	listed := readAddedColumnsFile(t)
	if !slices.IsSorted(listed) {
		t.Errorf("%s is not sorted; keep it sorted so two branches adding a line rarely touch the same place", addedColumnsFile)
	}
	inFile := map[string]bool{}
	for _, pair := range listed {
		if inFile[pair] {
			t.Errorf("%s lists %q twice", addedColumnsFile, pair)
		}
		inFile[pair] = true
	}

	inCode := map[string]bool{}
	for _, c := range addedColumns {
		pair := c.table + " " + c.column
		if inCode[pair] {
			t.Errorf("addedColumns has %s.%s twice", c.table, c.column)
		}
		inCode[pair] = true
		if !inFile[pair] {
			t.Errorf("addedColumns has %s.%s, which %s does not list yet: add the line %q to it, in order",
				c.table, c.column, addedColumnsFile, pair)
		}
	}
	for _, pair := range listed {
		if !inCode[pair] {
			table, column, _ := strings.Cut(pair, " ")
			t.Errorf("%s.%s is gone from addedColumns. Shipped migration entries are never removed: an install that "+
				"stopped before this column gains it only through that entry. Put the entry back; its line in %s stays",
				table, column, addedColumnsFile)
		}
	}
}

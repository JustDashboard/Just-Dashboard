package dbx

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseBracketList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		ok   bool
	}{
		{"", []string{}, true},
		{"[a]", []string{"a"}, true},
		{"[a], [b]", []string{"a", "b"}, true},
		{"[a],[b]", []string{"a", "b"}, true},
		{"[we,ird name], [x]]y]", []string{"we,ird name", "x]y"}, true},
		{"a, b", nil, false},
		{"[a", nil, false},
		{"[a] [b]", nil, false},
		{"[a],", nil, false},
	}
	for _, tc := range cases {
		got, ok := parseBracketList(tc.in)
		if ok != tc.ok || (ok && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("parseBracketList(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestMissingIndexStatement(t *testing.T) {
	d, err := DialectFor(DriverMSSQL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := missingIndexStatement(d, MissingIndex{
		Schema: "dbo", Table: "Orders", Equality: []string{"CustomerId"}, Inequality: []string{"Created At"},
		Include: []string{"Total", "Sta]tus"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE NONCLUSTERED INDEX [Orders_CustomerId_Created At_idx] ON [dbo].[Orders] ([CustomerId], [Created At]) INCLUDE ([Total], [Sta]]tus])"
	if got != want {
		t.Errorf("statement =\n%s\nwant\n%s", got, want)
	}
	plain, err := missingIndexStatement(d, MissingIndex{Schema: "dbo", Table: "t", Inequality: []string{"a"}})
	if err != nil || strings.Contains(plain, "INCLUDE") || !strings.Contains(plain, "([a])") {
		t.Errorf("statement without include = %q, %v", plain, err)
	}
	if _, err := missingIndexStatement(d, MissingIndex{Schema: "dbo", Table: "t", Include: []string{"a"}}); err == nil {
		t.Error("an entry with no key column made a statement")
	}
}

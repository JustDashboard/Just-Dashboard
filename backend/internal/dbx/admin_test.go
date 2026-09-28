package dbx

import "testing"

func TestMySQLAccountQuotesTheUserHalf(t *testing.T) {
	for user, want := range map[string]string{
		"app":            `'app'@'%'`,
		"o'brien":        `'o''brien'@'%'`,
		`x\' OR 1=1 -- `: `'x\\'' OR 1=1 -- '@'%'`,
	} {
		got, err := mysqlAccount(user, "")
		if err != nil {
			t.Fatalf("%q: %v", user, err)
		}
		if got != want {
			t.Errorf("mysqlAccount(%q) = %s, want %s", user, got, want)
		}
	}
	if _, err := mysqlAccount("app", "x' OR '1"); err == nil {
		t.Error("a quote in the host half was accepted")
	}
}

package store

import (
	"strings"
	"testing"
)

func TestFileVersionSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(fileVersionSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

func TestFileVersionTablesSurviveReopening(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec(`INSERT INTO file_versions(path, revision, size, at) VALUES('/srv/app.conf', 'abc', 3, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec(`INSERT INTO file_version_heads(path, revision, at) VALUES('/srv/app.conf', 'def', 2)`); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM file_versions JOIN file_version_heads USING(path)`).Scan(&n); err != nil || n != 1 {
		t.Fatal("reopening lost a file's versions", n, err)
	}
}

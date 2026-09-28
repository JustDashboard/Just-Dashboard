package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// ParseDotenv reads per entry now, so a preview can judge every line; a
// strict read must still stop at the first fault in the order it was written.
func TestParseDotenvReportsTheFirstFaultInDocumentOrder(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"1BAD=x\nA='open":      "invalid name",
		"A=1\nB='open":         "not closed",
		"A=1\nA=2\n1X=3":       "duplicate",
		"A=\"x\" junk\n1X=1":   "text after",
		"A=a\x00b\nA=1":        "value for A",
		"A=1\nB=2\nNO_EQUALS":  "line 3 needs NAME=value",
		"OK=1\n9=\"never ends": "invalid name",
	} {
		if _, err := ParseDotenv(input); !errors.Is(err, ErrInvalidVariable) || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseDotenv(%q) error = %v, want one naming %q", input, err, want)
		}
	}
}

// A preview judges every name against what is stored — by digest, on the
// server — and writes nothing. A fault that belongs to one name is that
// name's verdict; a fault the import would refuse as a whole is refused here
// with the same error.
func TestPreviewDotenvImportJudgesEveryNameAndWritesNothing(t *testing.T) {
	t.Parallel()
	fixture := newPlanningStoreFixture(t)
	ctx := context.Background()
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	seeded, err := fixture.plans.ImportDotenv(ctx, projectID, environmentID, "operator", DotenvImportRequest{
		Revision: 1, Dotenv: "SAME=one\nVALUE=old\n", Sensitivity: "secret", Scopes: []string{"runtime"},
	})
	if err != nil {
		t.Fatal(err)
	}
	preview := func(dotenv string, scopes ...string) (*DotenvImportPreview, error) {
		return fixture.plans.PreviewDotenvImport(ctx, projectID, environmentID, DotenvImportRequest{
			Revision: seeded.DesiredRevision, Dotenv: dotenv, Sensitivity: "secret", Scopes: scopes,
		})
	}

	judged, err := preview("SAME=one\nVALUE=new\n# a comment\nFRESH=1\n", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	want := []DotenvImportVerdict{
		{Name: "SAME", Line: 1, Change: "unchanged"},
		{Name: "VALUE", Line: 2, Change: "changed"},
		{Name: "FRESH", Line: 4, Change: "added"},
	}
	if !slices.Equal(judged.Variables, want) {
		t.Fatalf("verdicts = %+v, want %+v", judged.Variables, want)
	}
	rescoped, err := preview("SAME=one\n", "build", "runtime")
	if err != nil || rescoped.Variables[0].Change != "changed" {
		t.Fatalf("same value on new scopes = %+v, %v, want changed", rescoped, err)
	}

	refused, err := preview("OK=1\n1BAD=2\nTWICE=a\nTWICE=b\nBIN=a\x00b\n", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	want = []DotenvImportVerdict{
		{Name: "OK", Line: 1, Change: "added"},
		{Name: "1BAD", Line: 2, Change: "refused", Reason: dotenvRefusedName},
		{Name: "TWICE", Line: 3, Change: "refused", Reason: dotenvRefusedDuplicate},
		{Name: "BIN", Line: 5, Change: "refused", Reason: dotenvRefusedValue},
	}
	if !slices.Equal(refused.Variables, want) {
		t.Fatalf("refused verdicts = %+v, want %+v", refused.Variables, want)
	}

	for _, whole := range []struct {
		dotenv string
		scopes []string
		want   string
	}{
		{"A=1\nB='open", []string{"runtime"}, "line 2"},
		{"A=1\n", []string{"everywhere"}, "scope"},
		{"ALIAS=${{variable.MISSING}}\n", []string{"runtime"}, "MISSING"},
		{"# only a comment\n", []string{"runtime"}, "empty"},
	} {
		if _, err := preview(whole.dotenv, whole.scopes...); !errors.Is(err, ErrInvalidVariable) || !strings.Contains(err.Error(), whole.want) {
			t.Errorf("preview(%q) error = %v, want the import's refusal naming %q", whole.dotenv, err, whole.want)
		}
	}
	if _, err := preview("ALIAS=${{variable.SAME}}\n", "runtime"); err != nil {
		t.Fatalf("a reference to a stored variable was refused: %v", err)
	}

	variables, err := fixture.plans.ListVariables(ctx, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(variables) != 2 || variables[0].DesiredRevision != seeded.DesiredRevision {
		t.Fatalf("variables after previews = %+v, want the two seeded at revision %d", variables, seeded.DesiredRevision)
	}
	if value, err := fixture.plans.RevealVariable(ctx, projectID, environmentID, "VALUE"); err != nil || value.Value != "old" {
		t.Fatalf("VALUE after previews = %+v, %v, want the stored value untouched", value, err)
	}
}

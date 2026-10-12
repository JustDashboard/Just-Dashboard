package auth

import (
	"strings"
	"testing"
)

// Every account made from now on is asked what it is for, and answering —
// with sections hidden or with none — is what stops the question.
func TestNewAccountsOweOnboardingUntilTheyAnswer(t *testing.T) {
	svc, user := newTestService(t, false)
	if !user.NeedsOnboarding || user.HiddenSections == nil || len(user.HiddenSections) != 0 {
		t.Fatalf("new account: onboarding %v hidden %#v", user.NeedsOnboarding, user.HiddenSections)
	}

	hidden, err := svc.SetHiddenSections(t.Context(), user.ID, []string{"proxy", "docker", "proxy"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(hidden, ",") != "docker,proxy" {
		t.Fatalf("stored %v, want sorted without repeats", hidden)
	}
	got, err := svc.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NeedsOnboarding || strings.Join(got.HiddenSections, ",") != "docker,proxy" {
		t.Fatalf("after answering: onboarding %v hidden %v", got.NeedsOnboarding, got.HiddenSections)
	}

	// Showing everything again is an empty list, served as one.
	if _, err := svc.SetHiddenSections(t.Context(), user.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.UserByID(t.Context(), user.ID); got.HiddenSections == nil || len(got.HiddenSections) != 0 {
		t.Fatalf("cleared list read back as %#v", got.HiddenSections)
	}
}

func TestHiddenSectionsRefuseWhatIsNotASectionName(t *testing.T) {
	svc, user := newTestService(t, false)
	tooMany := make([]string, maxHiddenSections+1)
	for i := range tooMany {
		tooMany[i] = "docker"
	}
	for _, sections := range [][]string{{"Docker"}, {""}, {"../files"}, {"proxy/sites"}, {strings.Repeat("a", 33)}, tooMany} {
		if _, err := svc.SetHiddenSections(t.Context(), user.ID, sections); err == nil {
			t.Fatalf("%q accepted", sections)
		}
	}
	if got, _ := svc.UserByID(t.Context(), user.ID); !got.NeedsOnboarding {
		t.Fatal("a refused answer still ended onboarding")
	}
	if _, err := svc.SetHiddenSections(t.Context(), user.ID+100, []string{"docker"}); err != ErrNotFound {
		t.Fatalf("unknown account: %v", err)
	}
}

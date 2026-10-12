package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
)

// maxHiddenSections bounds the list well above the sidebar's own count of
// sections, so a request cannot store an arbitrary amount of text.
const maxHiddenSections = 32

// sectionKey is the shape of a section's name: the first segment of its path.
// The list of sections belongs to the frontend's nav registry, which ships in
// the same stack, so the server checks the shape rather than keeping a second
// copy of the list that one side could change without the other.
var sectionKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// SetHiddenSections records which sections the account keeps out of its
// sidebar and ends its first-run question: answering it, or skipping it with
// nothing hidden, is the same write. It returns the list as stored — sorted,
// without repeats — so the audit entry and the response agree.
func (s *Service) SetHiddenSections(ctx context.Context, userID int64, sections []string) ([]string, error) {
	if len(sections) > maxHiddenSections {
		return nil, fmt.Errorf("at most %d sections can be hidden", maxHiddenSections)
	}
	for _, key := range sections {
		if !sectionKey.MatchString(key) {
			return nil, fmt.Errorf("%q is not a section", key)
		}
	}
	// Copied into a non-nil slice so an empty list is stored and served as
	// `[]` rather than `null`.
	hidden := append([]string{}, sections...)
	slices.Sort(hidden)
	hidden = slices.Compact(hidden)
	encoded, err := json.Marshal(hidden)
	if err != nil {
		return nil, err
	}
	res, err := s.st.DB.ExecContext(ctx,
		`UPDATE users SET hidden_sections = ?, needs_onboarding = 0 WHERE id = ?`, string(encoded), userID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return hidden, nil
}

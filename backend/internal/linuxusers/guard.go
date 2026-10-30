package linuxusers

import (
	"context"
	"fmt"
)

// Change describes what is about to happen to an account, so its effect can be
// judged before anything is written. Only the fields that are set apply.
type Change struct {
	Username    string
	Delete      bool
	Password    *string
	Disabled    *bool
	Shell       *string
	Groups      *[]string
	KeysRemoved int
	// KeysLost is every key at once: a home changed without its files.
	KeysLost bool
	DropRule string
	// AddRule is what replaces DropRule.
	AddRule *SudoRule
	// NewName and PrimaryGroup are a profile change: sshd's AllowUsers and a %group
	// rule both read them.
	NewName      *string
	PrimaryGroup *string
	// Members rewrites one group's membership across every account.
	Group   string
	Members *[]string
}

// wayIn is every account that could sign in over SSH today and reach root from
// there. A route that asks for a password the account does not have is not one.
func wayIn(users []User) []string {
	out := []string{}
	for _, u := range users {
		if !u.SignIn.Any() {
			continue
		}
		for _, route := range u.RootRoutes {
			if route.Usable && !route.Limited {
				out = append(out, u.Username)
				break
			}
		}
	}
	return out
}

// applyChange is the reading with the change made, recomputed the same way a
// fresh reading would be.
func applyChange(st *accountState, change Change) []User {
	out := make([]User, 0, len(st.users))
	rules := st.rules
	if change.DropRule != "" && rules != nil {
		kept := make([]SudoRule, 0, len(rules))
		for _, rule := range rules {
			if rule.ID == change.DropRule {
				rule.Who = otherPeople(rule.Who, change.Username)
				if len(rule.Who) == 0 {
					continue
				}
			}
			kept = append(kept, rule)
		}
		rules = kept
	}
	if change.AddRule != nil && rules != nil {
		rules = append(append([]SudoRule{}, rules...), *change.AddRule)
	}
	if change.NewName != nil && rules != nil {
		// A rename moves the account's own rule with it and refuses the others, so
		// what stays named is what stays granted.
		renamed := make([]SudoRule, 0, len(rules))
		for _, rule := range rules {
			if contains(rule.Who, change.Username) {
				rule.Who = append(otherPeople(rule.Who, change.Username), *change.NewName)
			}
			renamed = append(renamed, rule)
		}
		rules = renamed
	}
	for _, u := range st.users {
		if u.Username == change.Username {
			if change.Delete {
				continue
			}
			if change.Password != nil {
				u.Password = *change.Password
				u.Locked = *change.Password == PasswordLocked || *change.Password == PasswordNone
				u.NoPassword = *change.Password == PasswordEmpty
			}
			if change.Disabled != nil {
				u.Disabled = *change.Disabled
			}
			if change.Shell != nil {
				u.Shell = *change.Shell
			}
			if change.Groups != nil {
				u.Groups = append([]string{}, *change.Groups...)
			}
			u.SSHKeys = max(u.SSHKeys-change.KeysRemoved, 0)
			if change.KeysLost {
				u.SSHKeys = 0
			}
			if change.PrimaryGroup != nil {
				groups := otherPeople(u.Groups, u.PrimaryGroup)
				u.Groups = append(groups, *change.PrimaryGroup)
				u.PrimaryGroup = *change.PrimaryGroup
			}
			if change.NewName != nil {
				u.Username = *change.NewName
			}
		}
		if change.Members != nil {
			groups := []string{}
			for _, group := range u.Groups {
				if group != change.Group {
					groups = append(groups, group)
				}
			}
			if contains(*change.Members, u.Username) {
				groups = append(groups, change.Group)
			}
			u.Groups = groups
		}
		u.RootRoutes = rootRoutesFor(u, rules)
		u.SignIn = evaluateSignIn(u, st.policy)
		out = append(out, u)
	}
	return out
}

// guard refuses a change that closes the last way in. A host that already had
// none has nothing left to lose, and an operator mending one must not be stopped
// by the state they are mending.
//
// The dashboard's own terminal is not counted, on purpose: it stops working the
// day the stack does, and that is the day somebody needs SSH.
func (s *Service) guard(ctx context.Context, change Change) error {
	st, err := s.read(ctx)
	if err != nil {
		return err
	}
	return judge(st, change)
}

func judge(st *accountState, change Change) error {
	before := wayIn(st.users)
	if len(before) == 0 {
		return nil
	}
	if len(wayIn(applyChange(st, change))) > 0 {
		return nil
	}
	return fmt.Errorf("%w: nobody else could sign in over SSH and become root. Give another administrator a key or a password first", ErrLastWayIn)
}

// WayIn names the accounts that could sign in and become root today.
func (s *Service) WayIn(ctx context.Context) ([]string, error) {
	st, err := s.read(ctx)
	if err != nil {
		return nil, err
	}
	return wayIn(st.users), nil
}

// Judge says whether a change would be refused, without making it.
func (s *Service) Judge(ctx context.Context, change Change) error {
	return s.guard(ctx, change)
}

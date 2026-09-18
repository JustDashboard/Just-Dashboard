package deploy

import (
	"fmt"
	"regexp"

	"golang.org/x/crypto/bcrypt"
)

// DomainProtection puts a password in front of a deployment's public route:
// a staging site nobody outside the team should see, a preview a customer
// should not stumble on. The proxy asks for HTTP basic credentials and the
// application never learns about it.
//
// The plan carries a bcrypt hash, never the password. Password is accepted
// on a write and sealed into Hash before anything is stored, so a plan read
// back, a release snapshot, a preview copy and the route the proxy renders
// all hold what an htpasswd file would hold and nothing more.
type DomainProtection struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Hash     string `json:"hash,omitempty"`
}

var (
	domainProtectionUserRE = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)
	bcryptHashRE           = regexp.MustCompile(`^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$`)
)

// sealDomainProtection replaces every plaintext password in the plan with
// its hash. It runs once, at the API boundary, before the plan is stored:
// a digest computed over the stored plan then stays the same on every read,
// which hashing on each canonicalisation (a fresh salt every time) would
// break between preflight and commit.
func sealDomainProtection(c *PlanConfiguration) error {
	for index := range c.Domains {
		protection := c.Domains[index].Protection
		if protection == nil || protection.Password == "" {
			continue
		}
		field := fmt.Sprintf("domains[%d].protection", index)
		// bcrypt silently truncates at 72 bytes, so a longer password would
		// quietly become a shorter one.
		if len(protection.Password) < 8 {
			return &ValidationError{Field: field, Message: "the password must be at least 8 characters"}
		}
		if len(protection.Password) > 72 {
			return &ValidationError{Field: field, Message: "the password must be at most 72 characters, which is bcrypt's limit"}
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(protection.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		protection.Hash, protection.Password = string(hash), ""
	}
	return nil
}

// validate is what a stored plan must satisfy: a legal user name and a
// bcrypt hash, and no password — one that reached this point unsealed is a
// plan that would have stored it.
func (p *DomainProtection) validate(field string) error {
	if p == nil {
		return nil
	}
	if !domainProtectionUserRE.MatchString(p.Username) {
		return &ValidationError{Field: field, Message: "the user name may use letters, digits, dots, dashes, underscores and @, up to 64 characters"}
	}
	if p.Password != "" {
		return &ValidationError{Field: field, Message: "the protection password must be sealed before the plan is stored"}
	}
	if !bcryptHashRE.MatchString(p.Hash) {
		return &ValidationError{Field: field, Message: "a protected domain needs a password"}
	}
	return nil
}

// canonicalDomainProtection drops an empty protection object, so a form that
// turned the switch off and left the fields blank stores no protection.
func canonicalDomainProtection(p *DomainProtection) *DomainProtection {
	if p == nil || (p.Username == "" && p.Password == "" && p.Hash == "") {
		return nil
	}
	copied := *p
	return &copied
}

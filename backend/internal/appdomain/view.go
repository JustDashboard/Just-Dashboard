package appdomain

// View is GET /dashboard/link/domain's answer, the shape the Settings page
// builds to. A field with nothing to say is null.
type View struct {
	// Enabled is JD_APP_DOMAINS (and JD_ACCOUNT_LINK, which it needs).
	Enabled bool `json:"enabled"`
	// Linked is the account link being active.
	Linked bool `json:"linked"`
	// Available is the site offering app domains, nil when unknown: not
	// linked, or the site could not be asked. Reason then says which.
	Available     *bool        `json:"available"`
	Zone          *string      `json:"zone"`
	Reason        *string      `json:"reason"`
	PublicAddress *string      `json:"publicAddress"`
	AutoAssign    bool         `json:"autoAssign"`
	Domain        *DomainView  `json:"domain"`
	Certificate   *Certificate `json:"certificate"`
	UsedBy        []Use        `json:"usedBy"`
	// Detached are deployment names under the zone that this server's
	// handle does not cover: a released, suspended or older handle's.
	Detached []Use `json:"detached"`
}

// DomainView is the handle this server holds, or held.
type DomainView struct {
	Handle   string `json:"handle"`
	Hostname string `json:"hostname"`
	Wildcard string `json:"wildcard"`
	Address  string `json:"address"`
	// Status is active, suspended or detached.
	Status    string  `json:"status"`
	Reason    *string `json:"reason"`
	UpdatedAt int64   `json:"updatedAt"`
}

// Certificate is the handle's wildcard.
type Certificate struct {
	Name string `json:"name"`
	// State is missing, issuing, valid, expiring or failed.
	State     string  `json:"state"`
	ExpiresAt *int64  `json:"expiresAt"`
	Error     *string `json:"error"`
	// JobID is the running or last issuance's job, when this process ran
	// one, for its transcript.
	JobID *string `json:"jobId"`
}

// Use is one deployment domain under the zone.
type Use struct {
	ProjectID     int64  `json:"projectId"`
	Project       string `json:"project"`
	EnvironmentID int64  `json:"environmentId"`
	Environment   string `json:"environment"`
	Hostname      string `json:"hostname"`
}

// Names is what a deployment page needs to place a hostname: the zone and,
// when this server holds a handle it may use, the base every app name ends
// in. GET /deploy/app-domain answers it to every account that reads
// deployments.
type Names struct {
	Enabled bool `json:"enabled"`
	// Zone is the app domain zone, nil when none is known.
	Zone *string `json:"zone"`
	// Base is <handle>.<zone> while the domain is active, else nil.
	Base       *string `json:"base"`
	Status     *string `json:"status"`
	AutoAssign bool    `json:"autoAssign"`
}

func ptr[T any](v T) *T { return &v }

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

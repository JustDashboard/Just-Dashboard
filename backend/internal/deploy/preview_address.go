package deploy

// PreviewAddress is where a preview environment answers. A "tailnet" address
// is a port on this host's Tailscale node that `tailscale serve` maps to the
// preview's loopback publication, so only the tailnet can reach it; a
// "domain" address is a webhook preview's public hostname pattern, kept for
// the previews that predate the tailnet path.
type PreviewAddress struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
	Port int    `json:"port,omitempty"`
	// UpstreamPort is the loopback port the tailnet mapping last pointed at.
	// It is what lets a republish tell "our own earlier mapping" apart from
	// something the operator served on the same port by hand.
	UpstreamPort int  `json:"-"`
	Published    bool `json:"published"`
}

// PreviewTarget is one open preview as the reconciler sees it: enough to ask
// GitHub about its pull request and to act on the answer.
type PreviewTarget struct {
	PreviewID     int64
	TriggerID     int64
	ProjectID     int64
	EnvironmentID int64
	Repository    string
	Number        int
	// Revision is the approved head the preview was configured at;
	// HeadRevision is the newest head the reconciler has seen on the pull
	// request, which differs from Revision once new commits arrive.
	Revision     string
	HeadRevision string
	Origin       string
	State        string
}

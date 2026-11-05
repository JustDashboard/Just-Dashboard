package accountlink

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"time"
)

// Profile is what makes one link differ from another. The engine in this
// package — the signed requests, the sequence, the command checks, the replay
// table, the loops, the backoff — is the same for every link; a profile says
// where one instance keeps its state, which paths it calls, what it calls the
// other end and how it is named in the audit log.
//
// Two instances run side by side: the account link to just-dashboard.com
// (AccountProfile) and the hub link (package hublink). They share no state:
// each has its own identity, sequence counter, link state, command table,
// update policy, loops, rate pacing, destructive-budget key and audit actor, so
// switching one off, unlinking it or losing its key never touches the other.
type Profile struct {
	// Name is the link's name in the log: "account link".
	Name string
	// Title finishes "linked to …" in the log: "a just-dashboard.com account".
	Title string
	// Peer is what messages call the other end ("site"), and PeerAt where a
	// decision about this server is made there ("on the site").
	Peer, PeerAt string
	// KeyPrefix starts every settings key the instance keeps: "account.link".
	KeyPrefix string
	// CommandsTable is the replay table, one the store creates. It is written
	// into queries, so it is never anything but a constant.
	CommandsTable string
	// BasePath is the path every call to the peer starts with, and part of
	// every signed string, so a signature for one link never verifies on the
	// other.
	BasePath string
	// Agent is the last word of the User-Agent: "account-link".
	Agent string
	// ActorPrefix makes the audit actor, and the destructive budget's key, from
	// the owner label: "account:" + "ion@example.com".
	ActorPrefix string
	// AuditPrefix starts the audit actions the engine writes: "account" makes
	// account.link, account.unlink, account.command.<kind>, account.update.auto.
	AuditPrefix string
	// HeartbeatsPerMinute is the peer's limit for one server, which the
	// heartbeat paces itself to.
	HeartbeatsPerMinute int
	// DefaultHeartbeatSeconds is used when an approval names no interval.
	DefaultHeartbeatSeconds int
	// KidPattern is what a key id must look like in an approval.
	KidPattern *regexp.Regexp
	// RotationKidPattern, when set, is what an announced key's id must look
	// like as well; unset, any id of 1 to 64 bytes is taken.
	RotationKidPattern *regexp.Regexp
	// OwnerPattern, when set, is what owner.label must match in an approval.
	OwnerPattern *regexp.Regexp
	// Discover asks GET <BasePath>/site, and checks the protocol it lists,
	// before a link request.
	Discover bool
	// OriginPerLink means the origin is chosen when a link is begun and
	// stored with the request and the link, instead of being fixed by
	// configuration (Options.SiteURL).
	OriginPerLink bool
	// ReplyView means the heartbeat reply carries fleet and notices, always,
	// and signIn, optionally. They are handed to Options.OnReply.
	ReplyView bool
	// IgnoreAnnouncedKeys means a heartbeat reply's siteKeys are decoded, so
	// strict decoding still passes, and never applied: no key is added, no
	// pinned key's end changes and none is dropped. A reply is authenticated
	// by TLS alone, and on a tailnet a machine name is a weak anchor for a key
	// that signs commands and sign-in tickets (hub-link-protocol.md 4.3).
	IgnoreAnnouncedKeys bool
	// ClockSequence raises the request counter to max(stored + 1, unix
	// milliseconds) instead of stored + 1, so a database restored from an
	// older copy carries on above every number it used before
	// (hub-link-protocol.md 3.1). The peer only checks "greater than".
	ClockSequence bool
}

// AccountProfile is the link to a just-dashboard.com account.
var AccountProfile = Profile{
	Name: "account link", Title: "a just-dashboard.com account",
	Peer: "site", PeerAt: "on the site",
	KeyPrefix: "account.link", CommandsTable: "account_link_commands",
	BasePath: basePath, Agent: "account-link",
	ActorPrefix: "account:", AuditPrefix: "account",
	HeartbeatsPerMinute: maxHeartbeatsPerMinute, DefaultHeartbeatSeconds: 30,
	KidPattern: kidPattern, Discover: true,
}

// Reply is what a heartbeat reply carries beyond the account link's fields, for
// a profile with ReplyView. Each part is the raw JSON the peer sent; Signin is
// nil when the peer left it out.
type Reply struct {
	Fleet, Notices, SignIn json.RawMessage
	// At is when the reply was received, on the protocol's clock.
	At time.Time
}

// ReplyWrite is what an instance keeps from a heartbeat reply, written inside
// the transaction that re-reads the link. What it returns, when not nil, runs
// once that transaction has committed.
type ReplyWrite func(ctx context.Context, tx *sql.Tx) (after func(), err error)

// Hooks are the points where an instance adds behaviour of its own. Each is
// optional.
type Hooks struct {
	// Admit says whether a stored link may run now. An error idles the link
	// (it is not cleared) and its text is shown as the last error. Without
	// it, a link runs only while its URL is Options.SiteURL.
	Admit func(ctx context.Context, st State) error
	// OnReply receives the view part of every successful heartbeat reply of a
	// ReplyView profile, before anything in the reply is used. An error fails
	// the heartbeat. What it returns, when not nil, is written in the
	// transaction that records the heartbeat, and only if the stored link is
	// still st: a reply that lands after its link ended writes nothing.
	OnReply func(ctx context.Context, st State, r Reply) (ReplyWrite, error)
	// OnClear runs inside the transaction that ends a link (unlink, removal by
	// the peer) or starts a new one, to delete what the instance keeps beside
	// the engine's own keys.
	OnClear func(ctx context.Context, tx *sql.Tx) error
	// HeartbeatExtra adds top-level keys to the heartbeat body. They are
	// never cut; the snapshot's lists are cut to leave room for them.
	HeartbeatExtra func(ctx context.Context, st State) (map[string]json.RawMessage, error)
	// BeforePoll runs before every poll of a pending request, in whichever
	// process polls it, and returns the request as it stands after it (it may
	// record something on it). An error sends nothing: a *RequestEndedError
	// ends the request as failed, anything else counts as a failed poll.
	BeforePoll func(ctx context.Context, req Request) (Request, error)
}

// RequestEndedError is a hook ending a pending request: nothing more is sent
// for it, and it ends as failed with Message.
type RequestEndedError struct{ Message string }

func (e *RequestEndedError) Error() string { return e.Message }

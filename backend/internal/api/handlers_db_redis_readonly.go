package api

import (
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// What a Redis request does to a connection that only reads may reach.
//
// A protected connection is guarded in one place, in front of every database
// route, and that guard sees a request before its handler does. Two Redis
// routes are a read or a write by what their body carries — the console and
// the bulk action — and for those the guard needs the answer the handler
// would reach. These give it, from the body alone.
//
// Each decodes the body with the handler's own decoder into the handler's own
// request type (protectedBody), so a field spelled in another case, or given
// twice, is read here exactly as it will be there: there is no second reading
// of the request for the two to disagree about.

// redisCommandWrites says why a console request is not a read, or nothing
// when it is one.
//
// Only the dashboard's own table is asked. A command it does not hold needs
// the server's word before it can be called a read, and a guard does not
// dial; such a command is refused, which is the side this may be wrong on.
// The handler asks the server and can find that a command the table calls a
// read is a write on this server, so it looks at the verdict again once it
// has one.
func redisCommandWrites(body []byte) string {
	var req redisCommandRequest
	if err := protectedBody(body, &req, false); err != nil {
		return "the request could not be read to see whether it changes anything"
	}
	args, err := dbx.RedisParseCommand(req.Command)
	if err != nil {
		return err.Error()
	}
	verdict := dbx.RedisClassify(args, nil)
	switch {
	case !verdict.Known:
		// Not named: what stands where a command should is whatever was
		// typed, and this sentence is recorded.
		return "that is not a command the dashboard knows to be a read"
	case verdict.Class != dbx.RedisClassRead:
		why := verdict.Name + " is not a read"
		if len(verdict.Reasons) > 0 {
			why += ": it " + strings.Join(verdict.Reasons, "; ")
		}
		return why
	}
	return ""
}

// redisBulkWrites says why a bulk request changes keys, or nothing when it
// only counts them.
func redisBulkWrites(body []byte) string {
	var req redisBulkRequest
	if err := protectedBody(body, &req, false); err != nil {
		return "the request could not be read to see whether it changes anything"
	}
	if !req.DryRun {
		return "a bulk " + defaultStr(req.Action, "action") + " changes every key the pattern matches; only its dry run leaves them as they are"
	}
	return ""
}

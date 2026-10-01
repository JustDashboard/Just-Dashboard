package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// Which MongoDB requests change nothing, judged from the request body.
//
// A connection can be marked protected, and the middleware that enforces it
// refuses every request that is not a GET unless its route is on a short
// list — with, for the routes that are a read or a write depending on what
// they carry, a check of the body. These are those checks for the Mongo
// routes. Each takes the raw body and returns nil when the request reads and
// an error saying why not otherwise, worded to follow "this connection is
// protected, and …".
//
// They read the body into the type the handler reads it into, with the
// decoder set as the handler's is, and then use the classifier the handler
// uses — so a request cannot be a read to one and a write to the other. The
// first half matters as much as the second: encoding/json matches a field
// name without regard to case and keeps the last one it finds, so a body
// read any other way (as a map of exact keys, say) is a different request
// from the one the handler runs as soon as a key appears twice in two
// spellings. And they fail closed: a body that cannot be read is not a read.

// mongoReadOnlyBody decodes a body the way httpx.DecodeJSON will when the
// handler asks.
func mongoReadOnlyBody(body []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("the request could not be read to see what it does")
	}
	return nil
}

// mongoReadOnlyPipeline allows POST /aggregate when the pipeline only reads.
func mongoReadOnlyPipeline(body []byte) error {
	var req mongoAggregateRequest
	if err := mongoReadOnlyBody(body, &req); err != nil {
		return err
	}
	info, err := dbx.MongoClassifyPipeline(req.Pipeline)
	if err != nil {
		return errors.New("the pipeline could not be read to see whether it writes")
	}
	if info.Writes {
		return fmt.Errorf("a pipeline with a %s stage writes to a collection", info.WriteStage)
	}
	if len(info.Unknown) > 0 {
		return fmt.Errorf("%s is not a stage known to only read", info.Unknown[0])
	}
	return nil
}

// mongoReadOnlyCommand allows POST /mongo/command when the command is one
// the console classifies as a read.
func mongoReadOnlyCommand(body []byte) error {
	var req mongoCommandRequest
	if err := mongoReadOnlyBody(body, &req); err != nil {
		return err
	}
	_, verdict, err := dbx.MongoClassifyCommand(req.Command)
	if err != nil {
		return errors.New("the command could not be read to see what it does")
	}
	if verdict.Class != dbx.MongoClassRead {
		return fmt.Errorf("%s is not a command that only reads", verdict.Command)
	}
	return nil
}

// mongoReadOnlyDryRun allows PATCH and DELETE /mongo/documents when the
// request only counts what it would reach. The two routes read their bodies
// into different types, so this reads the one field they share, named and
// typed as both have it.
func mongoReadOnlyDryRun(body []byte) error {
	var fields struct {
		DryRun bool `json:"dryRun"`
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		return errors.New("the request could not be read to see what it does")
	}
	if !fields.DryRun {
		return errors.New("only a dry run of this request leaves the documents alone")
	}
	return nil
}

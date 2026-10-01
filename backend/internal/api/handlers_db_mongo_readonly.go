package api

import (
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
// They use the classifiers the handlers use, so a request cannot be a read
// to one and a write to the other. And they fail closed: a body that cannot
// be read is not a read.

func mongoBodyField(body []byte, field string) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return "", errors.New("the request could not be read to see what it does")
	}
	raw, ok := fields[field]
	if !ok {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", fmt.Errorf("the request's %s could not be read to see what it does", field)
	}
	return text, nil
}

// mongoReadOnlyPipeline allows POST /aggregate when the pipeline only reads.
func mongoReadOnlyPipeline(body []byte) error {
	pipeline, err := mongoBodyField(body, "pipeline")
	if err != nil {
		return err
	}
	info, err := dbx.MongoClassifyPipeline(pipeline)
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
	command, err := mongoBodyField(body, "command")
	if err != nil {
		return err
	}
	_, verdict, err := dbx.MongoClassifyCommand(command)
	if err != nil {
		return errors.New("the command could not be read to see what it does")
	}
	if verdict.Class != dbx.MongoClassRead {
		return fmt.Errorf("%s is not a command that only reads", verdict.Command)
	}
	return nil
}

// mongoReadOnlyDryRun allows PATCH and DELETE /mongo/documents when the
// request only counts what it would reach.
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

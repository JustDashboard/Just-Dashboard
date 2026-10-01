package dbx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Validation: the rule a collection holds its documents to, and what that
// rule would say about the documents already there.
//
// A validator only judges writes. Adding one to a collection changes nothing
// about what is in it, and the first sign that existing documents fail is a
// later update being refused — so before a rule is saved, the page can ask
// how many documents it would reject, and see some of them.

// MongoValidation is a collection's validation rule.
type MongoValidation struct {
	// Validator is canonical Extended JSON, empty when there is none. It is
	// canonical because it is edited and sent back; ValidatorRelaxed is the
	// same rule for reading.
	Validator        string `json:"validator"`
	ValidatorRelaxed string `json:"validatorRelaxed"`
	// Level is strict (every insert and update), moderate (only documents
	// that already pass) or off.
	Level string `json:"level"`
	// Action is error (refuse the write) or warn (allow it and log).
	Action string `json:"action"`
}

// MongoReadValidation reads a collection's validator, level and action.
func MongoReadValidation(ctx context.Context, client *mongo.Client, dbName, collection string) (*MongoValidation, error) {
	if err := mongoNamespace(dbName, collection); err != nil {
		return nil, err
	}
	opts, kind, err := mongoCollectionOptions(ctx, client.Database(dbName), collection)
	if err != nil {
		return nil, err
	}
	if kind == "view" {
		return nil, fmt.Errorf("%s is a view; a view stores no documents to validate", collection)
	}
	// The server's defaults when a collection was made without saying.
	out := &MongoValidation{Level: "strict", Action: "error"}
	if doc, ok := opts.Lookup("validator").DocumentOK(); ok && !mongoIsEmpty(doc) {
		canonical, err := bson.MarshalExtJSON(doc, true, false)
		if err != nil {
			return nil, err
		}
		out.Validator, out.ValidatorRelaxed = string(canonical), mongoRelaxedJSON(doc)
	}
	if s, ok := opts.Lookup("validationLevel").StringValueOK(); ok {
		out.Level = s
	}
	if s, ok := opts.Lookup("validationAction").StringValueOK(); ok {
		out.Action = s
	}
	return out, nil
}

// mongoCollectionOptions returns one collection's options document and type.
func mongoCollectionOptions(ctx context.Context, db *mongo.Database, collection string) (bson.Raw, string, error) {
	cur, err := db.ListCollections(ctx, bson.D{{Key: "name", Value: collection}})
	if err != nil {
		return nil, "", err
	}
	defer cur.Close(context.Background())
	if !cur.Next(ctx) {
		if err := cur.Err(); err != nil {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("%w: %q in %s", ErrMongoNoCollection, collection, db.Name())
	}
	kind, _ := cur.Current.Lookup("type").StringValueOK()
	opts, _ := cur.Current.Lookup("options").DocumentOK()
	return append(bson.Raw(nil), opts...), kind, nil
}

// MongoWriteValidation sets a collection's validator, level and action. A
// nil validator leaves the rule alone and an empty one removes it; an empty
// level or action is left as it is.
func MongoWriteValidation(ctx context.Context, client *mongo.Client, dbName, collection string, validator *string, level, action string) error {
	return MongoModifyCollection(ctx, client, dbName, collection, MongoCollectionChange{
		Validator: validator, ValidationLevel: level, ValidationAction: action,
	})
}

// MongoValidationCheck is how a collection's documents fare against a rule.
type MongoValidationCheck struct {
	// Proposed is true when the rule checked was sent with the request
	// rather than read from the collection.
	Proposed bool `json:"proposed"`
	// Failing is how many documents the rule rejects. Exact is false when
	// counting took too long: Failing is then how many were found before
	// the time ran out, and there may be more.
	Failing int64 `json:"failing"`
	Exact   bool  `json:"exact"`
	// Total is the collection's estimated size, -1 when unknown.
	Total int64 `json:"total"`
	// Samples are a few of the failing documents.
	Samples    []MongoDoc `json:"samples"`
	DurationMs int64      `json:"durationMs"`
}

const (
	mongoValidationSamples    = 5
	mongoValidationMaxSamples = 50
)

// MongoCheckValidation counts the documents failing the collection's current
// validator, or a proposed one when validator is not empty, and returns a few
// of them.
func MongoCheckValidation(ctx context.Context, client *mongo.Client, dbName, collection, validator string, samples int, maxTimeMS int64) (*MongoValidationCheck, error) {
	if err := mongoNamespace(dbName, collection); err != nil {
		return nil, err
	}
	out := &MongoValidationCheck{Samples: []MongoDoc{}, Exact: true, Total: -1}
	var rule bson.Raw
	if strings.TrimSpace(validator) != "" {
		doc, err := mongoParseDocument("validator", validator)
		if err != nil {
			return nil, err
		}
		rule, out.Proposed = doc, true
	} else {
		current, err := MongoReadValidation(ctx, client, dbName, collection)
		if err != nil {
			return nil, err
		}
		if current.Validator != "" {
			if rule, err = mongoParseDocument("validator", current.Validator); err != nil {
				return nil, err
			}
		}
	}
	coll := client.Database(dbName).Collection(collection)
	if n, err := coll.EstimatedDocumentCount(ctx, options.EstimatedDocumentCount().SetMaxTime(mongoCountTime)); err == nil {
		out.Total = n
	}
	// No rule, or an empty one, rejects nothing.
	if mongoIsEmpty(rule) {
		return out, nil
	}
	// A validator is a query that passing documents match, so the failing
	// ones are those that do not.
	failing := bson.D{{Key: "$nor", Value: bson.A{rule}}}
	maxTime := mongoMaxTime(maxTimeMS)
	start := time.Now()
	n, err := coll.CountDocuments(ctx, failing, options.Count().SetMaxTime(maxTime))
	switch {
	case err == nil:
		out.Failing = n
	case MongoTimedOut(err):
		out.Exact = false
	default:
		return nil, err
	}
	if samples <= 0 {
		samples = mongoValidationSamples
	}
	if samples > mongoValidationMaxSamples {
		samples = mongoValidationMaxSamples
	}
	// The samples are looked for even when the count ran out of time: the
	// first failing document is usually found long before the last.
	if out.Failing > 0 || !out.Exact {
		cur, err := coll.Find(ctx, failing, options.Find().SetLimit(int64(samples)).SetMaxTime(maxTime))
		if err == nil {
			defer cur.Close(context.Background())
			page := &MongoFindResult{Documents: []MongoDoc{}}
			if err := mongoReadPage(ctx, cur, int64(samples), page); err == nil {
				out.Samples = page.Documents
			} else if out.Exact {
				return nil, err
			}
		} else if out.Exact {
			return nil, err
		}
		if !out.Exact && int64(len(out.Samples)) > out.Failing {
			out.Failing = int64(len(out.Samples))
		}
	}
	out.DurationMs = time.Since(start).Milliseconds()
	return out, nil
}

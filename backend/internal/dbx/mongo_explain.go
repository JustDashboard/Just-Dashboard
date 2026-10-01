package dbx

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo"
)

// Explain: what the server will do with a query, as a tree somebody can read.
//
// The reply to explain has had three shapes. The classic engine reports a
// tree of stages (IXSCAN under FETCH under SORT) and, when asked to execute,
// the same tree again with a count beside every stage. The slot-based engine
// reports that classic tree for the plan and an entirely different tree for
// what ran, the two linked only by a planNodeId. An aggregation reports a
// list of pipeline stages whose first holds a classic reply of its own. All
// three are folded here into one tree of one node type, with the figures
// attached wherever the reply had them; where it did not, the figure is
// absent rather than zero.

// MongoPlanNode is one stage of a plan.
type MongoPlanNode struct {
	// Stage is the server's name for it: COLLSCAN, IXSCAN, FETCH, SORT,
	// $group, and so on.
	Stage string `json:"stage"`
	// Index, KeyPattern, Direction and IndexBounds describe an index scan.
	Index       string `json:"index,omitempty"`
	KeyPattern  string `json:"keyPattern,omitempty"`
	Direction   string `json:"direction,omitempty"`
	IndexBounds string `json:"indexBounds,omitempty"`
	// Filter is the condition the stage applies, as relaxed Extended JSON.
	Filter string `json:"filter,omitempty"`
	// The figures below are present only when the plan was executed.
	Returned     *int64 `json:"returned"`
	DocsExamined *int64 `json:"docsExamined"`
	KeysExamined *int64 `json:"keysExamined"`
	// TimeMs is the server's estimate of time spent in this stage and the
	// stages under it.
	TimeMs   *int64          `json:"timeMs"`
	Children []MongoPlanNode `json:"children"`
}

// MongoExplainSummary is the plan in one line.
type MongoExplainSummary struct {
	Namespace string `json:"namespace"`
	// Verbosity is what was run, which may be less than what was asked.
	Verbosity string `json:"verbosity"`
	// Executed is true when the figures are real: the query was run.
	Executed bool `json:"executed"`
	// Engine is classic or sbe.
	Engine string `json:"engine"`

	Returned     *int64 `json:"returned"`
	KeysExamined *int64 `json:"keysExamined"`
	DocsExamined *int64 `json:"docsExamined"`
	TimeMs       *int64 `json:"timeMs"`

	// IndexesUsed are the indexes the winning plan scans.
	IndexesUsed []string `json:"indexesUsed"`
	// CollectionScan is true when the plan reads the whole collection.
	CollectionScan bool `json:"collectionScan"`
	// InMemorySort is true when the plan sorts instead of reading in order.
	InMemorySort  bool `json:"inMemorySort"`
	UsedDisk      bool `json:"usedDisk"`
	RejectedPlans int  `json:"rejectedPlans"`
}

// MongoExplainResult is a plan, its summary, and the server's reply.
type MongoExplainResult struct {
	Summary MongoExplainSummary `json:"summary"`
	Plan    MongoPlanNode       `json:"plan"`
	// Raw is the server's whole reply as relaxed Extended JSON.
	Raw string `json:"raw"`
	// Note says when the request was changed to keep it a read.
	Note string `json:"note,omitempty"`
}

// MongoExplainSpec is a find — or, when Pipeline is set, an aggregation — to
// explain.
type MongoExplainSpec struct {
	MongoFindSpec
	Pipeline     string `json:"pipeline"`
	AllowDiskUse bool   `json:"allowDiskUse"`
	// Verbosity is queryPlanner, executionStats or allPlansExecution.
	// queryPlanner plans without running; the other two run the query.
	Verbosity string `json:"verbosity"`
}

var mongoVerbosities = map[string]bool{"queryPlanner": true, "executionStats": true, "allPlansExecution": true}

// MongoExplain explains a find or an aggregation.
func MongoExplain(ctx context.Context, client *mongo.Client, dbName, collection string, spec MongoExplainSpec) (*MongoExplainResult, error) {
	if err := mongoNamespace(dbName, collection); err != nil {
		return nil, err
	}
	verbosity := spec.Verbosity
	if verbosity == "" {
		verbosity = "queryPlanner"
	}
	if !mongoVerbosities[verbosity] {
		return nil, fmt.Errorf("verbosity is queryPlanner, executionStats or allPlansExecution")
	}
	collationDoc, _, err := mongoCollation(spec.Collation)
	if err != nil {
		return nil, err
	}
	hint, err := mongoHint(spec.Hint)
	if err != nil {
		return nil, err
	}
	note := ""
	var inner bson.D
	if strings.TrimSpace(spec.Pipeline) != "" {
		stages, err := mongoParseArray("pipeline", spec.Pipeline)
		if err != nil {
			return nil, err
		}
		if err := mongoGuardPipeline(dbName, stages); err != nil {
			return nil, err
		}
		info := mongoClassifyStages(stages)
		if len(info.Unknown) > 0 {
			return nil, fmt.Errorf("stage %s is not one this page can explain", info.Unknown[0])
		}
		// Explaining a pipeline with execution runs it. One that writes is
		// only ever planned from here; the server draws the same line.
		if info.Writes && verbosity != "queryPlanner" {
			verbosity = "queryPlanner"
			note = "This pipeline ends in " + info.WriteStage + ", so it was planned but not run."
		}
		inner = bson.D{
			{Key: "aggregate", Value: collection},
			{Key: "pipeline", Value: mongoArray(stages)},
			{Key: "cursor", Value: bson.D{}},
		}
		if spec.AllowDiskUse {
			inner = append(inner, bson.E{Key: "allowDiskUse", Value: true})
		}
	} else {
		filter, err := mongoParseOptionalDocument("filter", spec.Filter)
		if err != nil {
			return nil, err
		}
		projection, err := mongoParseOptionalDocument("projection", spec.Projection)
		if err != nil {
			return nil, err
		}
		sortDoc, err := mongoParseOptionalDocument("sort", spec.Sort)
		if err != nil {
			return nil, err
		}
		if spec.Skip < 0 {
			return nil, fmt.Errorf("skip cannot be negative")
		}
		inner = bson.D{{Key: "find", Value: collection}, {Key: "filter", Value: filter}}
		if !mongoIsEmpty(projection) {
			inner = append(inner, bson.E{Key: "projection", Value: projection})
		}
		if !mongoIsEmpty(sortDoc) {
			inner = append(inner, bson.E{Key: "sort", Value: sortDoc})
		}
		if spec.Skip > 0 {
			inner = append(inner, bson.E{Key: "skip", Value: spec.Skip})
		}
		// The limit is part of the plan: a sorted query with one is a top-k
		// sort, without one a full sort.
		if spec.Limit > 0 {
			inner = append(inner, bson.E{Key: "limit", Value: spec.Limit})
		}
	}
	if collationDoc != nil {
		inner = append(inner, bson.E{Key: "collation", Value: collationDoc})
	}
	if hint != nil {
		inner = append(inner, bson.E{Key: "hint", Value: hint})
	}
	cmd := bson.D{
		{Key: "explain", Value: inner},
		{Key: "verbosity", Value: verbosity},
		{Key: "maxTimeMS", Value: mongoMaxTime(spec.MaxTimeMS).Milliseconds()},
	}
	raw, err := client.Database(dbName).RunCommand(ctx, cmd).Raw()
	if err != nil {
		return nil, err
	}
	res := mongoReadExplain(raw)
	res.Summary.Verbosity = verbosity
	res.Summary.Executed = res.Summary.Executed && verbosity != "queryPlanner"
	if res.Summary.Namespace == "" {
		res.Summary.Namespace = dbName + "." + collection
	}
	res.Note = note
	return res, nil
}

// mongoReadExplain folds an explain reply into a tree and a summary.
func mongoReadExplain(raw bson.Raw) *MongoExplainResult {
	res := &MongoExplainResult{Raw: mongoRelaxedJSON(raw)}
	res.Summary.IndexesUsed = []string{}
	res.Summary.Engine = "classic"
	if v, ok := raw.Lookup("explainVersion").StringValueOK(); ok && v == "2" {
		res.Summary.Engine = "sbe"
	}
	res.Plan = mongoExplainNode(raw, &res.Summary)
	mongoSummarisePlan(res.Plan, &res.Summary)
	sort.Strings(res.Summary.IndexesUsed)
	return res
}

// mongoExplainNode reads one reply — a whole explain, or the part of one a
// shard or a $cursor stage holds — into a node.
func mongoExplainNode(reply bson.Raw, sum *MongoExplainSummary) MongoPlanNode {
	// An aggregation the planner could not push down whole: a list of
	// stages, each fed by the one before it, the first holding the query.
	if stages, ok := reply.Lookup("stages").ArrayOK(); ok {
		values, _ := stages.Values()
		var node *MongoPlanNode
		for _, v := range values {
			doc, ok := v.DocumentOK()
			if !ok {
				continue
			}
			name := mongoFirstKey(doc)
			var next MongoPlanNode
			if name == "$cursor" {
				cursor, _ := doc.Lookup("$cursor").DocumentOK()
				next = mongoExplainNode(cursor, sum)
			} else {
				next = MongoPlanNode{Stage: name, Children: []MongoPlanNode{}}
				if spec, err := doc.LookupErr(name); err == nil {
					next.Filter = mongoValueJSON(spec, false)
				}
			}
			if v, err := doc.LookupErr("nReturned"); err == nil && v.IsNumber() {
				next.Returned = mongoIntPtr(v)
				// The last stage's count is the pipeline's.
				sum.Returned = next.Returned
				sum.Executed = true
			}
			if v, err := doc.LookupErr("executionTimeMillisEstimate"); err == nil && v.IsNumber() {
				next.TimeMs = mongoIntPtr(v)
				if sum.TimeMs == nil || *next.TimeMs > *sum.TimeMs {
					sum.TimeMs = next.TimeMs
				}
			}
			if used, _ := doc.Lookup("usedDisk").BooleanOK(); used {
				sum.UsedDisk = true
			}
			if node != nil {
				next.Children = append(next.Children, *node)
			}
			n := next
			node = &n
		}
		if node != nil {
			return *node
		}
	}
	// A sharded cluster: one reply per shard.
	if shards, ok := reply.Lookup("shards").DocumentOK(); ok {
		root := MongoPlanNode{Stage: "SHARDS", Children: []MongoPlanNode{}}
		elems, _ := shards.Elements()
		for _, e := range elems {
			doc, ok := e.Value().DocumentOK()
			if !ok {
				continue
			}
			child := mongoExplainNode(doc, sum)
			root.Children = append(root.Children, MongoPlanNode{
				Stage: "SHARD " + e.Key(), Children: []MongoPlanNode{child},
			})
		}
		return root
	}

	planner, _ := reply.Lookup("queryPlanner").DocumentOK()
	if ns, ok := planner.Lookup("namespace").StringValueOK(); ok && sum.Namespace == "" {
		sum.Namespace = ns
	}
	if rejected, ok := planner.Lookup("rejectedPlans").ArrayOK(); ok {
		if values, err := rejected.Values(); err == nil {
			sum.RejectedPlans += len(values)
		}
	}
	winning, _ := planner.Lookup("winningPlan").DocumentOK()
	// The slot-based engine keeps the tree an operator recognises under
	// queryPlan, beside its own.
	if classic, ok := winning.Lookup("queryPlan").DocumentOK(); ok {
		winning = classic
	}

	exec, hasExec := reply.Lookup("executionStats").DocumentOK()
	if !hasExec {
		if len(winning) == 0 {
			return MongoPlanNode{Stage: "UNKNOWN", Children: []MongoPlanNode{}}
		}
		return mongoPlanTree(winning, nil, 0)
	}
	sum.Executed = true
	mongoAddInt(&sum.Returned, exec, "nReturned")
	mongoAddInt(&sum.KeysExamined, exec, "totalKeysExamined")
	mongoAddInt(&sum.DocsExamined, exec, "totalDocsExamined")
	mongoAddInt(&sum.TimeMs, exec, "executionTimeMillis")
	stages, _ := exec.Lookup("executionStages").DocumentOK()
	// The classic engine's execution tree is the plan tree with figures in
	// it, so it is read on its own. The slot-based one is a different tree;
	// its figures are gathered by plan node and laid over the plan.
	if sum.Engine != "sbe" {
		if _, ok := stages.Lookup("stage").StringValueOK(); ok {
			return mongoPlanTree(stages, nil, 0)
		}
	}
	figures := map[int64]*mongoNodeFigures{}
	mongoGatherFigures(stages, figures, 0)
	if len(winning) == 0 {
		return MongoPlanNode{Stage: "UNKNOWN", Children: []MongoPlanNode{}}
	}
	return mongoPlanTree(winning, figures, 0)
}

type mongoNodeFigures struct {
	returned, timeMs, docs, keys *int64
}

// mongoGatherFigures walks the slot-based execution tree, top down, keeping
// for each plan node the count and time of the highest stage that belongs to
// it and the reads of every scan under it.
func mongoGatherFigures(stage bson.Raw, into map[int64]*mongoNodeFigures, depth int) {
	if len(stage) == 0 || depth > mongoMaxDepth {
		return
	}
	if v, err := stage.LookupErr("planNodeId"); err == nil && v.IsNumber() {
		id := mongoInt(v)
		f := into[id]
		if f == nil {
			f = &mongoNodeFigures{}
			into[id] = f
			if v, err := stage.LookupErr("nReturned"); err == nil && v.IsNumber() {
				f.returned = mongoIntPtr(v)
			}
			if v, err := stage.LookupErr("executionTimeMillisEstimate"); err == nil && v.IsNumber() {
				f.timeMs = mongoIntPtr(v)
			}
		}
		name, _ := stage.Lookup("stage").StringValueOK()
		switch name {
		case "scan", "seek":
			mongoAddInt(&f.docs, stage, "numReads")
		case "ixscan", "ixseek", "ixscan_generic":
			// An index stage counts its reads too, and reads one past the
			// last key to learn it is done; keysExamined is the figure the
			// classic engine reports.
			if _, err := stage.LookupErr("keysExamined"); err == nil {
				mongoAddInt(&f.keys, stage, "keysExamined")
			} else {
				mongoAddInt(&f.keys, stage, "numReads")
			}
		}
	}
	for _, child := range mongoPlanChildren(stage) {
		mongoGatherFigures(child, into, depth+1)
	}
}

func mongoAddTo(dst **int64, n int64) {
	if *dst == nil {
		v := int64(0)
		*dst = &v
	}
	**dst += n
}

func mongoAddInt(dst **int64, doc bson.Raw, key string) {
	if v, err := doc.LookupErr(key); err == nil && v.IsNumber() {
		mongoAddTo(dst, mongoInt(v))
	}
}

func mongoIntPtr(v bson.RawValue) *int64 {
	n := mongoInt(v)
	return &n
}

// mongoPlanChildren finds the stages feeding a stage. They sit under keys
// that end in Stage or Stages — inputStage, inputStages, outerStage,
// thenStage — and the set has grown with every engine, so the suffix is what
// is matched rather than a list.
func mongoPlanChildren(stage bson.Raw) []bson.Raw {
	elems, err := stage.Elements()
	if err != nil {
		return nil
	}
	out := []bson.Raw{}
	for _, e := range elems {
		key, v := e.Key(), e.Value()
		switch {
		case strings.HasSuffix(key, "Stage") && v.Type == bsontype.EmbeddedDocument:
			out = append(out, v.Document())
		case strings.HasSuffix(key, "Stages") && v.Type == bsontype.Array:
			values, _ := v.Array().Values()
			for _, item := range values {
				if doc, ok := item.DocumentOK(); ok {
					out = append(out, doc)
				}
			}
		case key == "shards" && v.Type == bsontype.Array:
			// A mongos plan lists its shards, each with a winning plan.
			values, _ := v.Array().Values()
			for _, item := range values {
				doc, ok := item.DocumentOK()
				if !ok {
					continue
				}
				if plan, ok := doc.Lookup("winningPlan").DocumentOK(); ok {
					out = append(out, plan)
				} else if plan, ok := doc.Lookup("executionStages").DocumentOK(); ok {
					out = append(out, plan)
				}
			}
		}
	}
	return out
}

func mongoPlanTree(stage bson.Raw, figures map[int64]*mongoNodeFigures, depth int) MongoPlanNode {
	node := MongoPlanNode{Children: []MongoPlanNode{}}
	node.Stage, _ = stage.Lookup("stage").StringValueOK()
	if node.Stage == "" {
		// A plan a view or a time-series collection rewrote, whose head is a
		// wrapper rather than a stage.
		if inner, ok := stage.Lookup("queryPlan").DocumentOK(); ok {
			return mongoPlanTree(inner, figures, depth)
		}
		node.Stage = "UNKNOWN"
	}
	node.Index, _ = stage.Lookup("indexName").StringValueOK()
	node.Direction, _ = stage.Lookup("direction").StringValueOK()
	for field, dst := range map[string]*string{
		"keyPattern": &node.KeyPattern, "indexBounds": &node.IndexBounds, "filter": &node.Filter,
	} {
		if doc, ok := stage.Lookup(field).DocumentOK(); ok && !mongoIsEmpty(doc) {
			*dst = mongoRelaxedJSON(doc)
		}
	}
	for field, dst := range map[string]**int64{
		"nReturned": &node.Returned, "docsExamined": &node.DocsExamined,
		"keysExamined": &node.KeysExamined, "executionTimeMillisEstimate": &node.TimeMs,
	} {
		if v, err := stage.LookupErr(field); err == nil && v.IsNumber() {
			*dst = mongoIntPtr(v)
		}
	}
	if figures != nil {
		if v, err := stage.LookupErr("planNodeId"); err == nil && v.IsNumber() {
			if f := figures[mongoInt(v)]; f != nil {
				node.Returned, node.TimeMs = f.returned, f.timeMs
				node.DocsExamined, node.KeysExamined = f.docs, f.keys
			}
		}
	}
	if depth < mongoMaxDepth {
		for _, child := range mongoPlanChildren(stage) {
			node.Children = append(node.Children, mongoPlanTree(child, figures, depth+1))
		}
	}
	return node
}

// mongoSummarisePlan reads off the tree what the summary says about it.
func mongoSummarisePlan(node MongoPlanNode, sum *MongoExplainSummary) {
	switch {
	case node.Stage == "COLLSCAN":
		sum.CollectionScan = true
	case node.Stage == "SORT" || node.Stage == "$sort":
		sum.InMemorySort = true
	}
	if node.Index != "" {
		seen := false
		for _, name := range sum.IndexesUsed {
			seen = seen || name == node.Index
		}
		if !seen {
			sum.IndexesUsed = append(sum.IndexesUsed, node.Index)
		}
	}
	for _, child := range node.Children {
		mongoSummarisePlan(child, sum)
	}
}

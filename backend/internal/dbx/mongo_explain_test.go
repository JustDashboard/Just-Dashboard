package dbx

import (
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func explainFrom(t *testing.T, ext string) *MongoExplainResult {
	t.Helper()
	var raw bson.Raw
	if err := bson.UnmarshalExtJSON([]byte(ext), false, &raw); err != nil {
		t.Fatalf("test reply is not Extended JSON: %v", err)
	}
	return mongoReadExplain(raw)
}

func explainFile(t *testing.T, name string) *MongoExplainResult {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return explainFrom(t, string(data))
}

// The classic engine's reply for a find that was run: the execution tree is
// the plan tree with a count beside every stage.
const classicFindExplain = `{
  "explainVersion": "1",
  "queryPlanner": {
    "namespace": "shop.orders",
    "winningPlan": {"stage": "SORT", "sortPattern": {"total": -1}, "inputStage": {"stage": "FETCH",
      "filter": {"status": {"$eq": "paid"}},
      "inputStage": {"stage": "IXSCAN", "keyPattern": {"customer": 1}, "indexName": "customer_1",
        "direction": "forward", "indexBounds": {"customer": ["[7, 7]"]}}}},
    "rejectedPlans": [{"stage": "COLLSCAN"}, {"stage": "FETCH"}]
  },
  "executionStats": {
    "nReturned": 12, "executionTimeMillis": 9, "totalKeysExamined": 40, "totalDocsExamined": 40,
    "executionStages": {"stage": "SORT", "nReturned": 12, "executionTimeMillisEstimate": 8, "usedDisk": false,
      "inputStage": {"stage": "FETCH", "nReturned": 12, "executionTimeMillisEstimate": 6, "docsExamined": 40,
        "filter": {"status": {"$eq": "paid"}},
        "inputStage": {"stage": "IXSCAN", "nReturned": 40, "executionTimeMillisEstimate": 2, "keysExamined": 40,
          "keyPattern": {"customer": 1}, "indexName": "customer_1", "direction": "forward",
          "indexBounds": {"customer": ["[7, 7]"]}}}}
  }
}`

func TestExplainClassicFind(t *testing.T) {
	res := explainFrom(t, classicFindExplain)
	s := res.Summary
	if s.Engine != "classic" || !s.Executed || s.Namespace != "shop.orders" || s.RejectedPlans != 2 {
		t.Errorf("summary = %+v", s)
	}
	if deref(s.Returned) != 12 || deref(s.KeysExamined) != 40 || deref(s.DocsExamined) != 40 || deref(s.TimeMs) != 9 {
		t.Errorf("figures = %d returned, %d keys, %d docs, %d ms", deref(s.Returned), deref(s.KeysExamined), deref(s.DocsExamined), deref(s.TimeMs))
	}
	if s.CollectionScan || !s.InMemorySort || len(s.IndexesUsed) != 1 || s.IndexesUsed[0] != "customer_1" {
		t.Errorf("summary = %+v", s)
	}
	if res.Plan.Stage != "SORT" || len(res.Plan.Children) != 1 || deref(res.Plan.TimeMs) != 8 {
		t.Fatalf("root = %+v", res.Plan)
	}
	fetch := res.Plan.Children[0]
	if fetch.Stage != "FETCH" || deref(fetch.DocsExamined) != 40 || deref(fetch.Returned) != 12 || fetch.Filter != `{"status":{"$eq":"paid"}}` {
		t.Errorf("fetch = %+v", fetch)
	}
	scan := fetch.Children[0]
	if scan.Stage != "IXSCAN" || scan.Index != "customer_1" || scan.Direction != "forward" || deref(scan.KeysExamined) != 40 ||
		scan.KeyPattern != `{"customer":1}` || scan.IndexBounds != `{"customer":["[7, 7]"]}` || len(scan.Children) != 0 {
		t.Errorf("scan = %+v", scan)
	}
}

// Planned, not run: the tree is there and the figures are not, rather than
// being zero.
func TestExplainWithoutExecutionHasNoFigures(t *testing.T) {
	res := explainFrom(t, `{"explainVersion":"1","queryPlanner":{"namespace":"a.b",
	  "winningPlan":{"stage":"COLLSCAN","filter":{"n":{"$gt":1}},"direction":"forward"},"rejectedPlans":[]}}`)
	if res.Summary.Executed || res.Summary.Returned != nil || res.Summary.DocsExamined != nil || res.Summary.TimeMs != nil {
		t.Errorf("summary = %+v", res.Summary)
	}
	if !res.Summary.CollectionScan || res.Plan.Stage != "COLLSCAN" || res.Plan.Returned != nil || res.Plan.Filter == "" {
		t.Errorf("plan = %+v", res.Plan)
	}
}

// The slot-based engine's replies, as a 7.0 server gave them: the plan is
// under queryPlan, the execution tree is a different one, and the two meet
// only at planNodeId.
func TestExplainSlotBasedFind(t *testing.T) {
	res := explainFile(t, "mongo_explain_sbe_find.json")
	s := res.Summary
	if s.Engine != "sbe" || !s.Executed || !s.InMemorySort || s.CollectionScan || len(s.IndexesUsed) != 1 || s.IndexesUsed[0] != "n_1" {
		t.Errorf("summary = %+v", s)
	}
	if deref(s.Returned) != 40 || deref(s.KeysExamined) != 40 || deref(s.DocsExamined) != 40 {
		t.Errorf("figures = %d returned, %d keys, %d docs", deref(s.Returned), deref(s.KeysExamined), deref(s.DocsExamined))
	}
	if res.Plan.Stage != "SORT" || deref(res.Plan.Returned) != 40 {
		t.Fatalf("root = %+v", res.Plan)
	}
	fetch := res.Plan.Children[0]
	if fetch.Stage != "FETCH" || deref(fetch.DocsExamined) != 40 || deref(fetch.Returned) != 40 {
		t.Errorf("fetch = %+v", fetch)
	}
	scan := fetch.Children[0]
	if scan.Stage != "IXSCAN" || scan.Index != "n_1" || deref(scan.KeysExamined) != 40 || deref(scan.Returned) != 40 {
		t.Errorf("scan = %+v", scan)
	}
}

func TestExplainSlotBasedGroup(t *testing.T) {
	res := explainFile(t, "mongo_explain_sbe_group.json")
	if res.Plan.Stage != "GROUP" || deref(res.Plan.Returned) != 3 || deref(res.Summary.Returned) != 3 {
		t.Errorf("root = %+v, summary %+v", res.Plan, res.Summary)
	}
	if !planHas(res.Plan, "IXSCAN") || res.Summary.InMemorySort {
		t.Errorf("plan = %+v", res.Plan)
	}
}

// An aggregation the planner did not push down whole: a list of stages, the
// first holding the query, each later one fed by the one before.
func TestExplainAggregationStages(t *testing.T) {
	res := explainFrom(t, `{"explainVersion":"1","stages":[
	  {"$cursor":{"queryPlanner":{"namespace":"a.c","winningPlan":{"stage":"COLLSCAN"},"rejectedPlans":[]},
	    "executionStats":{"nReturned":50,"executionTimeMillis":3,"totalKeysExamined":0,"totalDocsExamined":50,
	      "executionStages":{"stage":"COLLSCAN","nReturned":50,"docsExamined":50,"executionTimeMillisEstimate":1}}},
	   "nReturned":50,"executionTimeMillisEstimate":2},
	  {"$group":{"_id":"$k","n":{"$sum":1}},"usedDisk":true,"nReturned":3,"executionTimeMillisEstimate":4},
	  {"$sort":{"sortKey":{"n":-1}},"nReturned":3,"executionTimeMillisEstimate":5}
	]}`)
	s := res.Summary
	if !s.Executed || deref(s.Returned) != 3 || deref(s.DocsExamined) != 50 || !s.CollectionScan || !s.InMemorySort || !s.UsedDisk || s.Namespace != "a.c" {
		t.Errorf("summary = %+v (returned %d, docs %d)", s, deref(s.Returned), deref(s.DocsExamined))
	}
	// Outermost first: the last stage is the root and the query the leaf.
	if res.Plan.Stage != "$sort" || res.Plan.Children[0].Stage != "$group" || res.Plan.Children[0].Children[0].Stage != "COLLSCAN" {
		t.Errorf("plan = %+v", res.Plan)
	}
	if deref(res.Plan.Children[0].Returned) != 3 || res.Plan.Children[0].Filter == "" {
		t.Errorf("group stage = %+v", res.Plan.Children[0])
	}
}

func TestExplainSharded(t *testing.T) {
	res := explainFrom(t, `{"shards":{
	  "rs0":{"queryPlanner":{"namespace":"a.c","winningPlan":{"stage":"IXSCAN","indexName":"k_1"},"rejectedPlans":[]},
	    "executionStats":{"nReturned":4,"totalKeysExamined":4,"totalDocsExamined":0,"executionTimeMillis":1,
	      "executionStages":{"stage":"IXSCAN","indexName":"k_1","nReturned":4,"keysExamined":4}}},
	  "rs1":{"queryPlanner":{"namespace":"a.c","winningPlan":{"stage":"COLLSCAN"},"rejectedPlans":[]},
	    "executionStats":{"nReturned":6,"totalKeysExamined":0,"totalDocsExamined":90,"executionTimeMillis":2,
	      "executionStages":{"stage":"COLLSCAN","nReturned":6,"docsExamined":90}}}}}`)
	if res.Plan.Stage != "SHARDS" || len(res.Plan.Children) != 2 || res.Plan.Children[0].Stage != "SHARD rs0" {
		t.Fatalf("plan = %+v", res.Plan)
	}
	s := res.Summary
	if deref(s.Returned) != 10 || deref(s.DocsExamined) != 90 || deref(s.KeysExamined) != 4 || !s.CollectionScan || len(s.IndexesUsed) != 1 {
		t.Errorf("summary = %+v (returned %d, docs %d, keys %d)", s, deref(s.Returned), deref(s.DocsExamined), deref(s.KeysExamined))
	}
}

// A reply with nothing recognisable in it is a plan nobody can read, not a
// crash.
func TestExplainUnreadableReply(t *testing.T) {
	res := explainFrom(t, `{"ok":1}`)
	if res.Plan.Stage != "UNKNOWN" || res.Summary.Executed || res.Plan.Children == nil || res.Summary.IndexesUsed == nil {
		t.Errorf("result = %+v", res)
	}
}

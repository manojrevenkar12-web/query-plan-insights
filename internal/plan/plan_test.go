package plan

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func load(t *testing.T, name string) *Node {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestParseKeepsWhatThePlannerChose(t *testing.T) {
	n := load(t, "index_scan.json")
	if n.Type != "Limit" || len(n.Children) != 1 {
		t.Fatalf("root %+v", n)
	}
	heap := n.Children[0].Children[0]
	if heap.Type != "Bitmap Heap Scan" || heap.Relation != "orders" || heap.Schema != "shop" {
		t.Fatalf("heap scan %+v", heap)
	}
	if heap.IndexCond != "(orders.customer_id = $1)" {
		t.Fatalf("a bitmap heap scan's recheck condition stands in for its index condition: %q", heap.IndexCond)
	}
	if got := n.Children[0].SortKey; !reflect.DeepEqual(got, []string{"orders.created_at DESC"}) {
		t.Fatalf("sort key %v", got)
	}
}

func TestParseRejectsWhatIsNotAPlan(t *testing.T) {
	for _, in := range []string{``, `{}`, `[]`, `[{"Plan": {}}]`, `[{"Plan": {"Node Type": 3}}]`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) accepted", in)
		}
	}
}

func TestFingerprintIgnoresCostsAndEstimates(t *testing.T) {
	a := load(t, "index_scan.json")
	b := load(t, "index_scan.json")
	b.Walk(func(x *Node) { x.Cost *= 100; x.Rows += 5000; x.Filter = "(other)" })
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("cost, rows and filter text must not change the fingerprint")
	}
	if a.Fingerprint() == load(t, "seq_scan.json").Fingerprint() {
		t.Fatal("an index scan and a sequential scan must differ")
	}
	if len(a.Fingerprint()) != 12 {
		t.Fatalf("fingerprint %q", a.Fingerprint())
	}
}

func TestRelationsIndexesAndAccess(t *testing.T) {
	n := load(t, "hash_join.json")
	if got := n.Relations(); !reflect.DeepEqual(got, []string{"shop.order_items", "shop.orders"}) {
		t.Fatalf("relations %v", got)
	}
	if got := n.Indexes(); !reflect.DeepEqual(got, []string{"orders_created_idx"}) {
		t.Fatalf("indexes %v", got)
	}
	want := map[string]string{"shop.order_items": "Seq Scan", "shop.orders": "Index Scan using orders_created_idx"}
	if got := n.Access(); !reflect.DeepEqual(got, want) {
		t.Fatalf("access %v", got)
	}
}

func TestDiffNamesTheAccessChange(t *testing.T) {
	got := Diff(load(t, "index_scan.json"), load(t, "seq_scan.json"))
	if len(got) != 1 {
		t.Fatalf("changes %+v", got)
	}
	c := got[0]
	if c.Kind != "access" || c.Relation != "shop.orders" || c.Before != "Bitmap Heap Scan" || c.After != "Seq Scan" {
		t.Fatalf("change %+v", c)
	}
	if d := Diff(load(t, "seq_scan.json"), load(t, "seq_scan.json")); len(d) != 0 {
		t.Fatalf("a plan does not differ from itself: %+v", d)
	}
}

func TestDiffSeesJoinAndAggregationChanges(t *testing.T) {
	before := load(t, "hash_join.json")
	after := load(t, "hash_join.json")
	after.Strategy = "Sorted"
	after.Children[0].Type = "Nested Loop"
	kinds := map[string]Change{}
	for _, c := range Diff(before, after) {
		kinds[c.Kind] = c
	}
	if c := kinds["join"]; c.Before != "Hash Join" || c.After != "Nested Loop" {
		t.Fatalf("join change %+v", c)
	}
	if c := kinds["aggregation"]; !strings.HasPrefix(c.Before, "Hashed Aggregate") || !strings.HasPrefix(c.After, "Sorted Aggregate") {
		t.Fatalf("aggregation change %+v", c)
	}
}

func TestDiffAgainstNoPlan(t *testing.T) {
	got := Diff(load(t, "index_scan.json"), &Node{Type: "Result"})
	if len(got) != 2 || got[0].After != "not read" || got[1].Kind != "aggregation" || got[1].Before != "Sort" {
		t.Fatalf("changes %+v", got)
	}
}

func TestFilterColumnsAreIndexCandidates(t *testing.T) {
	got := load(t, "seq_scan.json").FilterColumns()
	if !reflect.DeepEqual(got, map[string][]string{"shop.orders": {"customer_id"}}) {
		t.Fatalf("filter columns %v", got)
	}
	if got := load(t, "hash_join.json").FilterColumns(); len(got) != 0 {
		t.Fatalf("only sequential scans give candidates: %v", got)
	}
}

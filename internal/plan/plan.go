// Package plan parses PostgreSQL EXPLAIN (FORMAT JSON) output, fingerprints a plan's shape and diffs two plans.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Node is one plan node with the fields the analysis needs.
type Node struct {
	Type      string   `json:"type"`
	Relation  string   `json:"relation,omitempty"`
	Schema    string   `json:"schema,omitempty"`
	Index     string   `json:"index,omitempty"`
	JoinType  string   `json:"joinType,omitempty"`
	Strategy  string   `json:"strategy,omitempty"`
	Filter    string   `json:"filter,omitempty"`
	IndexCond string   `json:"indexCond,omitempty"`
	SortKey   []string `json:"sortKey,omitempty"`
	Rows      float64  `json:"rows"`
	Cost      float64  `json:"cost"`
	Children  []*Node  `json:"children,omitempty"`
}

type rawNode struct {
	NodeType     string    `json:"Node Type"`
	RelationName string    `json:"Relation Name"`
	Schema       string    `json:"Schema"`
	IndexName    string    `json:"Index Name"`
	JoinType     string    `json:"Join Type"`
	Strategy     string    `json:"Strategy"`
	Filter       string    `json:"Filter"`
	IndexCond    string    `json:"Index Cond"`
	RecheckCond  string    `json:"Recheck Cond"`
	SortKey      []string  `json:"Sort Key"`
	PlanRows     float64   `json:"Plan Rows"`
	TotalCost    float64   `json:"Total Cost"`
	Plans        []rawNode `json:"Plans"`
}

// Parse reads the JSON EXPLAIN produces: an array with one object holding "Plan".
func Parse(data []byte) (*Node, error) {
	var top []struct {
		Plan rawNode `json:"Plan"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("explain json: %w", err)
	}
	if len(top) == 0 || top[0].Plan.NodeType == "" {
		return nil, fmt.Errorf("explain json: no plan")
	}
	return convert(top[0].Plan), nil
}

func convert(r rawNode) *Node {
	cond := r.IndexCond
	if cond == "" {
		cond = r.RecheckCond
	}
	n := &Node{Type: r.NodeType, Relation: r.RelationName, Schema: r.Schema, Index: r.IndexName, JoinType: r.JoinType,
		Strategy: r.Strategy, Filter: r.Filter, IndexCond: cond, SortKey: r.SortKey, Rows: r.PlanRows, Cost: r.TotalCost}
	for _, c := range r.Plans {
		n.Children = append(n.Children, convert(c))
	}
	return n
}

// Shape is the plan without costs, row estimates or parameter values: what the planner chose, not what it guessed.
func (n *Node) Shape() string {
	var b strings.Builder
	var walk func(*Node, int)
	walk = func(x *Node, depth int) {
		fmt.Fprintf(&b, "%d:%s|%s|%s|%s|%s;", depth, x.Type, x.Relation, x.Index, x.JoinType, x.Strategy)
		for _, c := range x.Children {
			walk(c, depth+1)
		}
	}
	walk(n, 0)
	return b.String()
}

// Fingerprint is a short hash of the shape.
func (n *Node) Fingerprint() string {
	sum := sha256.Sum256([]byte(n.Shape()))
	return hex.EncodeToString(sum[:6])
}

// Walk visits every node depth-first.
func (n *Node) Walk(f func(*Node)) {
	f(n)
	for _, c := range n.Children {
		c.Walk(f)
	}
}

// Relations lists the qualified relations the plan reads.
func (n *Node) Relations() []string {
	seen := map[string]bool{}
	n.Walk(func(x *Node) {
		if x.Relation != "" {
			seen[qualify(x.Schema, x.Relation)] = true
		}
	})
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// Indexes lists the indexes the plan uses.
func (n *Node) Indexes() []string {
	seen := map[string]bool{}
	n.Walk(func(x *Node) {
		if x.Index != "" {
			seen[x.Index] = true
		}
	})
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func qualify(schema, rel string) string {
	if schema == "" {
		return rel
	}
	return schema + "." + rel
}

// Change is one difference between two plans, in words a developer reads.
type Change struct {
	Kind     string `json:"kind"`
	Relation string `json:"relation,omitempty"`
	Before   string `json:"before"`
	After    string `json:"after"`
}

// Access describes how each relation is read: "Index Scan using orders_customer_idx", "Seq Scan".
func (n *Node) Access() map[string]string {
	out := map[string]string{}
	n.Walk(func(x *Node) {
		if x.Relation == "" {
			return
		}
		a := x.Type
		if x.Index != "" {
			a += " using " + x.Index
		}
		out[qualify(x.Schema, x.Relation)] = a
	})
	return out
}

// Diff explains what changed between two plans for the same query.
func Diff(before, after *Node) []Change {
	var out []Change
	ba, aa := before.Access(), after.Access()
	rels := map[string]bool{}
	for r := range ba {
		rels[r] = true
	}
	for r := range aa {
		rels[r] = true
	}
	names := make([]string, 0, len(rels))
	for r := range rels {
		names = append(names, r)
	}
	sort.Strings(names)
	for _, r := range names {
		if ba[r] != aa[r] {
			out = append(out, Change{Kind: "access", Relation: r, Before: orNone(ba[r]), After: orNone(aa[r])})
		}
	}
	bj, aj := joins(before), joins(after)
	if strings.Join(bj, ",") != strings.Join(aj, ",") {
		out = append(out, Change{Kind: "join", Before: strings.Join(bj, ", "), After: strings.Join(aj, ", ")})
	}
	bs, as := spills(before), spills(after)
	if bs != as {
		out = append(out, Change{Kind: "aggregation", Before: bs, After: as})
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "not read"
	}
	return s
}

func joins(n *Node) []string {
	var out []string
	n.Walk(func(x *Node) {
		if strings.Contains(x.Type, "Join") || x.Type == "Nested Loop" {
			out = append(out, x.Type)
		}
	})
	return out
}

func spills(n *Node) string {
	var out []string
	n.Walk(func(x *Node) {
		if x.Type == "Aggregate" || x.Type == "Sort" || x.Type == "Hash" {
			s := x.Type
			if x.Strategy != "" {
				s = x.Strategy + " " + s
			}
			out = append(out, s)
		}
	})
	return strings.Join(out, ", ")
}

var columnInFilter = regexp.MustCompile(`\(?\(?([a-z_][a-z0-9_]*)\)?\s*(=|<|>|<=|>=|~~|IN|= ANY)`)

// FilterColumns returns the columns a sequential scan filters on, per relation: index candidates.
func (n *Node) FilterColumns() map[string][]string {
	out := map[string][]string{}
	n.Walk(func(x *Node) {
		if x.Type != "Seq Scan" || x.Filter == "" {
			return
		}
		seen := map[string]bool{}
		for _, m := range columnInFilter.FindAllStringSubmatch(x.Filter, -1) {
			col := m[1]
			if !seen[col] {
				seen[col] = true
				out[qualify(x.Schema, x.Relation)] = append(out[qualify(x.Schema, x.Relation)], col)
			}
		}
	})
	return out
}

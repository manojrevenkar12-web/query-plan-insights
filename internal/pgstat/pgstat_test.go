package pgstat

import "testing"

func TestOnlyReadsAreExplained(t *testing.T) {
	cases := map[string]bool{
		"SELECT id FROM shop.orders WHERE customer_id = $1":         true,
		"  with recent AS (SELECT 1) SELECT * FROM recent":          true,
		"UPDATE shop.products SET stock = stock - $2 WHERE id = $1": false,
		"INSERT INTO t VALUES ($1)":                                 false,
		"SELECT queryid FROM pg_stat_statements":                    false,
		"SELECT relname FROM pg_catalog.pg_class":                   false,
		"BEGIN": false,
		"":      false,
	}
	for q, want := range cases {
		if got := Explainable(q); got != want {
			t.Errorf("Explainable(%q) = %v", q, got)
		}
	}
}

package agent

import "sort"

// ComputeMST returns a minimum spanning tree (well, _a_ spanning forest if the
// graph is disconnected) over the given undirected edges. Tiebreaker is the
// lexicographic order of (A, B), so every node — given the same edge set —
// computes the same tree.
//
// All edges have weight 1; for our overlay this is fine because the underlay
// is a WG mesh and we don't have meaningful latency/cost data to break ties.
func ComputeMST(nodes []string, edges []Edge) []Edge {
	sorted := append([]Edge(nil), edges...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].A != sorted[j].A {
			return sorted[i].A < sorted[j].A
		}
		return sorted[i].B < sorted[j].B
	})

	uf := newUnionFind(nodes)
	mst := make([]Edge, 0, len(nodes))
	for _, e := range sorted {
		if uf.union(e.A, e.B) {
			mst = append(mst, e)
		}
	}
	return mst
}

// LocalNeighbors returns nodes adjacent to me in the given spanning tree.
func LocalNeighbors(me string, mst []Edge) []string {
	set := map[string]struct{}{}
	for _, e := range mst {
		switch me {
		case e.A:
			set[e.B] = struct{}{}
		case e.B:
			set[e.A] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type unionFind struct {
	parent map[string]string
	rank   map[string]int
}

func newUnionFind(nodes []string) *unionFind {
	uf := &unionFind{
		parent: make(map[string]string, len(nodes)),
		rank:   make(map[string]int, len(nodes)),
	}
	for _, n := range nodes {
		uf.parent[n] = n
	}
	return uf
}

func (u *unionFind) find(x string) string {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
		return x
	}
	if u.parent[x] != x {
		u.parent[x] = u.find(u.parent[x])
	}
	return u.parent[x]
}

// union merges the sets containing a and b. Returns true if they were
// previously disjoint (i.e., the caller should keep this edge in the MST).
func (u *unionFind) union(a, b string) bool {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return false
	}
	if u.rank[ra] < u.rank[rb] {
		u.parent[ra] = rb
	} else if u.rank[ra] > u.rank[rb] {
		u.parent[rb] = ra
	} else {
		u.parent[rb] = ra
		u.rank[ra]++
	}
	return true
}

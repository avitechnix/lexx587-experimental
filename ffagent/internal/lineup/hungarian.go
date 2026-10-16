package lineup

import "math"

// maxWeightAssignment solves the rectangular assignment problem, returning
// for each row the assigned column (or -1) so that the total weight is
// maximized. Weights of math.Inf(-1) mark forbidden pairs.
func maxWeightAssignment(w [][]float64) []int {
	rows := len(w)
	if rows == 0 {
		return nil
	}
	cols := len(w[0])
	// Square min-cost problem of size rows+cols: each real row also gets a
	// private "unassigned" column, and dummy rows soak up unused columns.
	// Every row can always go unassigned, so forbidden pairs are never needed.
	n := rows + cols

	hi := 0.0
	for _, r := range w {
		for _, x := range r {
			if !math.IsInf(x, -1) && x > hi {
				hi = x
			}
		}
	}
	forbidden := hi*float64(n+1) + 1e6
	cost := make([][]float64, n+1)
	for i := range cost {
		cost[i] = make([]float64, n+1)
	}
	for i := 1; i <= rows; i++ {
		for j := 1; j <= n; j++ {
			switch {
			case j > cols:
				cost[i][j] = hi // unassigned: weight 0
			case math.IsInf(w[i-1][j-1], -1):
				cost[i][j] = forbidden
			default:
				cost[i][j] = hi - w[i-1][j-1]
			}
		}
	}
	// Dummy rows (i > rows) cost 0 everywhere.

	// Classic O(n^3) Hungarian algorithm with potentials (1-indexed).
	u := make([]float64, n+1)
	v := make([]float64, n+1)
	p := make([]int, n+1)
	way := make([]int, n+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, n+1)
		used := make([]bool, n+1)
		for j := range minv {
			minv[j] = math.Inf(1)
		}
		for {
			used[j0] = true
			i0 := p[j0]
			delta := math.Inf(1)
			j1 := 0
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				cur := cost[i0][j] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j] = cur
					way[j] = j0
				}
				if minv[j] < delta {
					delta = minv[j]
					j1 = j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
			if j0 == 0 {
				break
			}
		}
	}

	out := make([]int, rows)
	for i := range out {
		out[i] = -1
	}
	for j := 1; j <= n; j++ {
		i := p[j]
		if i >= 1 && i <= rows && j <= cols && !math.IsInf(w[i-1][j-1], -1) {
			out[i-1] = j - 1
		}
	}
	return out
}

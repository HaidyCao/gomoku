package game

// Renju forbidden-move (禁手) detection. These rules apply to BLACK only and are
// only consulted when a game has Forbidden enabled.
//
// This is a STRICT (recursive) implementation following the standard renju
// definitions:
//
//   - 连五 (exact five) always wins and is never forbidden — it takes precedence
//     over every forbidden shape, at every level of the recursion.
//   - 长连 (overline, six or more) is forbidden.
//   - 四四 (double four): the move creates two or more fours (on different lines).
//     A "four" is a line that becomes an exact five by adding one stone; the
//     completing move makes a five (a win) so it is always legal — fours need no
//     recursive check.
//   - 三三 (double three): the move creates two or more *open* threes. A line is a
//     real open three only if it can be turned into a *straight four* (活四,
//     `.BBBB.`) by adding a stone at a point that is itself NOT a forbidden move.
//     This legality test recurses, which is what distinguishes the strict rule
//     from a naive shape match: a three whose only straight-four completion is a
//     forbidden point (e.g. that point is itself a 四四) does not count, so the
//     move may be legal even though it looks like a 三三.
//
// The recursion terminates because every recursive call places one more black
// stone on a finite board; maxForbiddenDepth is a defensive cap that is never
// reached by positions realizable in normal play.

// wallColor marks an off-board cell inside a fixed-width scan window so the shape
// detectors never need per-cell bounds checks. It can never equal a real stone.
const wallColor Color = "#"

// forbiddenRadius is how far either side of a stone we examine. A five, overline,
// four, or straight-four completion through a point all fit within ±5 cells.
const forbiddenRadius = 5

// maxForbiddenDepth bounds the three-legality recursion. Real positions never
// nest more than a few levels; beyond the cap a completing move is treated as
// legal (the three counts), which only matters for unrealizable deep positions.
const maxForbiddenDepth = 8

// lineDirections are the four axes (horizontal, vertical, both diagonals) shared
// with CheckWin.
var lineDirections = [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}

// FiveStatus classifies the longest same-color run through a freshly placed stone.
type FiveStatus int

const (
	FiveNone     FiveStatus = iota // no run reaches five
	FiveExact                      // longest run is exactly five (a win)
	FiveOverline                   // longest run is six or more (长连)
)

// Reasons reported by EvaluateForbidden.
const (
	ForbiddenOverline    = "overline"
	ForbiddenDoubleFour  = "double_four"
	ForbiddenDoubleThree = "double_three"
)

// ForbiddenResult reports whether a black move sits on a forbidden point and why.
type ForbiddenResult struct {
	Forbidden bool
	Reason    string
}

func inBounds(row, col int) bool {
	return row >= 0 && row < BoardSize && col >= 0 && col < BoardSize
}

// lineWindow returns the cells along (dr,dc) centered on (row,col) at offsets
// [-radius..radius]; off-board cells become wallColor. The center index is radius.
func lineWindow(board [][]Color, row, col, dr, dc, radius int) []Color {
	cells := make([]Color, 2*radius+1)
	for i := -radius; i <= radius; i++ {
		r := row + dr*i
		c := col + dc*i
		if !inBounds(r, c) {
			cells[i+radius] = wallColor
			continue
		}
		cells[i+radius] = board[r][c]
	}
	return cells
}

// runThroughCenter measures the maximal run of color that includes the center
// cell of the window, returning its length and the inclusive [start,end] window
// indices. If the center cell is not color it returns a zero-length run.
func runThroughCenter(cells []Color, color Color) (length, start, end int) {
	center := len(cells) / 2
	if cells[center] != color {
		return 0, center, center
	}
	start = center
	for start-1 >= 0 && cells[start-1] == color {
		start--
	}
	end = center
	for end+1 < len(cells) && cells[end+1] == color {
		end++
	}
	return end - start + 1, start, end
}

// windowPoints converts a run of count cells starting at window index start
// (along dr,dc, centered on row,col) back into board coordinates.
func windowPoints(row, col, dr, dc, start, count int) []Point {
	points := make([]Point, 0, count)
	for i := start; i < start+count; i++ {
		off := i - forbiddenRadius
		points = append(points, Point{Row: row + dr*off, Col: col + dc*off})
	}
	return points
}

// classifyFive inspects the four axes through (row,col) for a run that includes
// the stone just placed. An exact five anywhere wins outright and is returned
// immediately; otherwise an overline (six or more) is reported. color must be the
// color just placed at (row,col).
func classifyFive(board [][]Color, row, col int, color Color) (FiveStatus, []Point) {
	hasOverline := false
	var overlineLine []Point
	for _, d := range lineDirections {
		cells := lineWindow(board, row, col, d[0], d[1], forbiddenRadius)
		length, start, _ := runThroughCenter(cells, color)
		if length == 5 {
			return FiveExact, windowPoints(row, col, d[0], d[1], start, 5)
		}
		if length >= 6 && !hasOverline {
			hasOverline = true
			overlineLine = windowPoints(row, col, d[0], d[1], start, length)
		}
	}
	if hasOverline {
		return FiveOverline, overlineLine
	}
	return FiveNone, nil
}

// windowHasFour reports whether filling one empty cell makes an exact five through
// the center stone — i.e. the center stone is part of a four along this line.
func windowHasFour(cells []Color, color Color) bool {
	center := len(cells) / 2
	for e := center - 4; e <= center+4; e++ {
		if e < 0 || e >= len(cells) || cells[e] != Empty {
			continue
		}
		cells[e] = color
		n, _, _ := runThroughCenter(cells, color)
		cells[e] = Empty
		if n == 5 {
			return true
		}
	}
	return false
}

// windowStraightFourCompletions returns the window indices of empty cells that,
// when filled, turn the center stone's line into a straight four (活四): a run of
// exactly four with both immediate ends empty. These are the candidate points
// that would complete an open three through the center.
func windowStraightFourCompletions(cells []Color, color Color) []int {
	center := len(cells) / 2
	var comps []int
	for e := center - 4; e <= center+4; e++ {
		if e < 0 || e >= len(cells) || cells[e] != Empty {
			continue
		}
		cells[e] = color
		n, start, end := runThroughCenter(cells, color)
		straight := n == 4 && start-1 >= 0 && end+1 < len(cells) && cells[start-1] == Empty && cells[end+1] == Empty
		cells[e] = Empty
		if straight {
			comps = append(comps, e)
		}
	}
	return comps
}

// countFours counts the axes on which black at (row,col) participates in a four.
func countFours(board [][]Color, row, col int) int {
	n := 0
	for _, d := range lineDirections {
		cells := lineWindow(board, row, col, d[0], d[1], forbiddenRadius)
		if windowHasFour(cells, Black) {
			n++
		}
	}
	return n
}

// countOpenThrees counts the axes on which black at (row,col) forms a real open
// three (one completable to a straight four by a legal move).
func countOpenThrees(board [][]Color, row, col, depth int) int {
	n := 0
	for _, d := range lineDirections {
		if hasRealOpenThree(board, row, col, d[0], d[1], depth) {
			n++
		}
	}
	return n
}

// hasRealOpenThree reports whether the black stone at (row,col) forms an open
// three along (dr,dc) that can be completed into a straight four by at least one
// LEGAL (non-forbidden) move. The legality test recurses through evalForbidden.
func hasRealOpenThree(board [][]Color, row, col, dr, dc, depth int) bool {
	cells := lineWindow(board, row, col, dr, dc, forbiddenRadius)
	for _, e := range windowStraightFourCompletions(cells, Black) {
		off := e - forbiddenRadius
		qr, qc := row+dr*off, col+dc*off
		if !inBounds(qr, qc) || board[qr][qc] != Empty {
			continue
		}
		board[qr][qc] = Black
		forbidden, _ := evalForbidden(board, qr, qc, depth+1)
		board[qr][qc] = Empty
		if !forbidden {
			return true
		}
	}
	return false
}

// evalForbidden reports whether black playing at (row,col) is a forbidden move.
// It assumes board[row][col] is already Black (the caller places it). It is the
// recursive core: open-three legality calls back into it for completing points.
func evalForbidden(board [][]Color, row, col, depth int) (bool, string) {
	if depth > maxForbiddenDepth {
		return false, ""
	}
	switch status, _ := classifyFive(board, row, col, Black); status {
	case FiveExact:
		return false, "" // 连五 wins; never forbidden
	case FiveOverline:
		return true, ForbiddenOverline
	}
	if countFours(board, row, col) >= 2 {
		return true, ForbiddenDoubleFour
	}
	if countOpenThrees(board, row, col, depth) >= 2 {
		return true, ForbiddenDoubleThree
	}
	return false, ""
}

// EvaluateForbidden reports whether the black stone just placed at (row,col) sits
// on a renju forbidden point. The caller must have already placed black there and
// is responsible for letting an exact five win first; this performs the full
// recursive 长连 / 四四 / 三三 analysis. Only black is ever restricted.
func EvaluateForbidden(board [][]Color, row, col int) ForbiddenResult {
	forbidden, reason := evalForbidden(board, row, col, 0)
	return ForbiddenResult{Forbidden: forbidden, Reason: reason}
}

// ForbiddenPoint marks a currently-forbidden cell for black and why.
type ForbiddenPoint struct {
	Row    int    `json:"row"`
	Col    int    `json:"col"`
	Reason string `json:"reason"`
}

// ForbiddenPointsForBlack scans every empty cell and returns those where black
// playing would be a forbidden move (a move making an exact five is a win, so it
// is never reported). It mutates the board transiently but restores it, so the
// caller should pass a board it owns. Intended to be called only when 禁手 is
// enabled and it is black's turn — it powers both the board's ✕ warnings and the
// forbiddenPoints the agent reads over the API.
func ForbiddenPointsForBlack(board [][]Color) []ForbiddenPoint {
	var points []ForbiddenPoint
	for r := 0; r < BoardSize; r++ {
		for c := 0; c < BoardSize; c++ {
			if board[r][c] != Empty {
				continue
			}
			board[r][c] = Black
			forbidden, reason := evalForbidden(board, r, c, 0)
			board[r][c] = Empty
			if forbidden {
				points = append(points, ForbiddenPoint{Row: r, Col: c, Reason: reason})
			}
		}
	}
	return points
}

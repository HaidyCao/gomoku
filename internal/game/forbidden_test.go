package game

import (
	"testing"
	"time"
)

func boardWith(spec map[Color][]Point) [][]Color {
	board := make([][]Color, BoardSize)
	for r := range board {
		board[r] = make([]Color, BoardSize)
	}
	for color, points := range spec {
		for _, p := range points {
			board[p.Row][p.Col] = color
		}
	}
	return board
}

func TestClassifyFive(t *testing.T) {
	exact := boardWith(map[Color][]Point{Black: {{7, 3}, {7, 4}, {7, 5}, {7, 6}, {7, 7}}})
	if status, line := classifyFive(exact, 7, 7, Black); status != FiveExact || len(line) != 5 {
		t.Fatalf("exact five: status=%d line=%v", status, line)
	}

	overline := boardWith(map[Color][]Point{Black: {{7, 3}, {7, 4}, {7, 5}, {7, 6}, {7, 7}, {7, 8}}})
	if status, _ := classifyFive(overline, 7, 8, Black); status != FiveOverline {
		t.Fatalf("overline: status=%d, want FiveOverline", status)
	}

	none := boardWith(map[Color][]Point{Black: {{7, 5}, {7, 6}, {7, 7}}})
	if status, _ := classifyFive(none, 7, 7, Black); status != FiveNone {
		t.Fatalf("three: status=%d, want FiveNone", status)
	}
}

func TestCountFours(t *testing.T) {
	cases := []struct {
		name  string
		black []Point
		at    Point
		want  int
	}{
		{"XXXX_", []Point{{7, 3}, {7, 4}, {7, 5}, {7, 6}}, Point{7, 6}, 1},
		{"XX_XX", []Point{{7, 3}, {7, 4}, {7, 6}, {7, 7}}, Point{7, 7}, 1},
		{"X_XXX", []Point{{7, 3}, {7, 5}, {7, 6}, {7, 7}}, Point{7, 7}, 1},
		{"open_three_is_not_a_four", []Point{{7, 5}, {7, 6}, {7, 7}}, Point{7, 7}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			board := boardWith(map[Color][]Point{Black: tc.black})
			if got := countFours(board, tc.at.Row, tc.at.Col); got != tc.want {
				t.Fatalf("countFours = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCountOpenThrees(t *testing.T) {
	cases := []struct {
		name  string
		black []Point
		white []Point
		at    Point
		want  int
	}{
		{"_XXX_", []Point{{7, 5}, {7, 6}, {7, 7}}, nil, Point{7, 7}, 1},
		{"_X_XX_", []Point{{7, 4}, {7, 6}, {7, 7}}, nil, Point{7, 7}, 1},
		{"_XX_X_", []Point{{7, 4}, {7, 5}, {7, 7}}, nil, Point{7, 7}, 1},
		{"blocked_three", []Point{{7, 5}, {7, 6}, {7, 7}}, []Point{{7, 4}}, Point{7, 7}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			board := boardWith(map[Color][]Point{Black: tc.black, White: tc.white})
			if got := countOpenThrees(board, tc.at.Row, tc.at.Col, 0); got != tc.want {
				t.Fatalf("countOpenThrees = %d, want %d", got, tc.want)
			}
		})
	}
}

// gameWithStones builds a playing game with pre-placed stones. The move history
// is synthetic (parity is not enforced) — only the resulting board matters, which
// mirrors how TestApplyMoveDraw seeds a position before a final ApplyMove.
func gameWithStones(forbidden bool, black, white []Point) *Game {
	g := &Game{
		HumanColor: Black,
		AgentColor: White,
		Status:     StatusPlaying,
		Forbidden:  forbidden,
	}
	add := func(points []Point, color Color, player Player) {
		for _, p := range points {
			g.Moves = append(g.Moves, Move{
				MoveNumber: len(g.Moves) + 1,
				Row:        p.Row,
				Col:        p.Col,
				Color:      color,
				Player:     player,
			})
		}
	}
	add(black, Black, Human)
	add(white, White, Agent)
	g.MoveCount = len(g.Moves)
	return g
}

func TestForbiddenOverlineBlackLoses(t *testing.T) {
	now := time.Now()
	g := gameWithStones(true, []Point{{7, 3}, {7, 4}, {7, 5}, {7, 6}, {7, 8}}, nil)
	g.NextColor = Black
	if _, err := ApplyMove(g, 7, 7, Human, now); err != nil {
		t.Fatalf("apply move: %v", err)
	}
	if g.Status != StatusWhiteWon || g.EndReason != EndReasonForbidden || g.WinnerColor != White {
		t.Fatalf("black overline should lose: status=%s reason=%s winner=%s", g.Status, g.EndReason, g.WinnerColor)
	}
}

func TestForbiddenDoubleThreeBlackLoses(t *testing.T) {
	now := time.Now()
	g := gameWithStones(true, []Point{{7, 5}, {7, 6}, {5, 7}, {6, 7}}, nil)
	g.NextColor = Black
	if _, err := ApplyMove(g, 7, 7, Human, now); err != nil {
		t.Fatalf("apply move: %v", err)
	}
	if g.Status != StatusWhiteWon || g.EndReason != EndReasonForbidden {
		t.Fatalf("black double-three should lose: status=%s reason=%s", g.Status, g.EndReason)
	}
}

func TestForbiddenDoubleFourBlackLoses(t *testing.T) {
	now := time.Now()
	g := gameWithStones(true, []Point{{7, 4}, {7, 5}, {7, 6}, {4, 7}, {5, 7}, {6, 7}}, nil)
	g.NextColor = Black
	if _, err := ApplyMove(g, 7, 7, Human, now); err != nil {
		t.Fatalf("apply move: %v", err)
	}
	if g.Status != StatusWhiteWon || g.EndReason != EndReasonForbidden {
		t.Fatalf("black double-four should lose: status=%s reason=%s", g.Status, g.EndReason)
	}
}

// TestStrictReleasesFakeDoubleThree is the signature strict-vs-naive case. Black
// at (7,7) forms a vertical open three (5,7)(6,7)(7,7) and a horizontal jump
// three (7,4)(7,5)_(7,7). The horizontal three's ONLY straight-four completion is
// (7,6) — but (7,6) is itself a 四四 point (it completes both a vertical four on
// column 6 and the horizontal four), so it is forbidden. A naive shape checker
// counts two threes and wrongly forbids (7,7); the strict rule discards the fake
// horizontal three (no legal completion), leaving one real three, so (7,7) is
// LEGAL and the game continues.
func TestStrictReleasesFakeDoubleThree(t *testing.T) {
	now := time.Now()
	g := gameWithStones(true,
		[]Point{{7, 4}, {7, 5}, {5, 7}, {6, 7}, {4, 6}, {5, 6}, {6, 6}}, nil)
	g.NextColor = Black
	if _, err := ApplyMove(g, 7, 7, Human, now); err != nil {
		t.Fatalf("apply move: %v", err)
	}
	if g.Status != StatusPlaying || g.NextColor != White {
		t.Fatalf("strict rule should release the fake 三三 (legal move): status=%s next=%s", g.Status, g.NextColor)
	}
}

// TestStrictDoubleThreeStillForbiddenWhenCompletionsLegal is the contrast to
// TestStrictReleasesFakeDoubleThree: the SAME two threes, but without the column-6
// stones the completion (7,6) is an ordinary legal four, so both threes are real
// and (7,7) is a genuine 三三 — black loses.
func TestStrictDoubleThreeStillForbiddenWhenCompletionsLegal(t *testing.T) {
	now := time.Now()
	g := gameWithStones(true, []Point{{7, 4}, {7, 5}, {5, 7}, {6, 7}}, nil)
	g.NextColor = Black
	if _, err := ApplyMove(g, 7, 7, Human, now); err != nil {
		t.Fatalf("apply move: %v", err)
	}
	if g.Status != StatusWhiteWon || g.EndReason != EndReasonForbidden {
		t.Fatalf("genuine 三三 should lose: status=%s reason=%s", g.Status, g.EndReason)
	}
}

func TestForbiddenPointsForBlack(t *testing.T) {
	// Black has two pending threes crossing at (7,7); (7,7) is a 三三 point.
	board := boardWith(map[Color][]Point{Black: {{7, 5}, {7, 6}, {5, 7}, {6, 7}}})
	points := ForbiddenPointsForBlack(board)

	found := false
	for _, p := range points {
		if p.Row == 7 && p.Col == 7 {
			found = true
			if p.Reason != ForbiddenDoubleThree {
				t.Fatalf("(7,7) reason = %q, want %q", p.Reason, ForbiddenDoubleThree)
			}
		}
		if p.Row == 0 && p.Col == 0 {
			t.Fatalf("(0,0) should not be forbidden")
		}
	}
	if !found {
		t.Fatalf("expected (7,7) among forbidden points, got %+v", points)
	}

	// The board must be left untouched after scanning.
	for _, p := range []Point{{7, 5}, {7, 6}, {5, 7}, {6, 7}} {
		if board[p.Row][p.Col] != Black {
			t.Fatalf("board mutated at %v", p)
		}
	}
	if board[7][7] != Empty {
		t.Fatalf("board[7][7] should remain empty after scan")
	}
}

func TestExactFiveWinsEvenWithOpenThree(t *testing.T) {
	now := time.Now()
	// Horizontal makes an exact five; vertical would otherwise be an open three.
	g := gameWithStones(true, []Point{{7, 3}, {7, 4}, {7, 5}, {7, 6}, {5, 7}, {6, 7}}, nil)
	g.NextColor = Black
	if _, err := ApplyMove(g, 7, 7, Human, now); err != nil {
		t.Fatalf("apply move: %v", err)
	}
	if g.Status != StatusBlackWon || g.EndReason != EndReasonFiveInRow {
		t.Fatalf("exact five should win for black: status=%s reason=%s", g.Status, g.EndReason)
	}
}

func TestWhiteOverlineStillWinsWithForbidden(t *testing.T) {
	now := time.Now()
	g := gameWithStones(true, nil, []Point{{7, 3}, {7, 4}, {7, 5}, {7, 6}, {7, 8}})
	g.NextColor = White
	if _, err := ApplyMove(g, 7, 7, Agent, now); err != nil {
		t.Fatalf("apply move: %v", err)
	}
	if g.Status != StatusWhiteWon || g.EndReason != EndReasonFiveInRow || g.WinnerColor != White {
		t.Fatalf("white overline should win: status=%s reason=%s winner=%s", g.Status, g.EndReason, g.WinnerColor)
	}
}

func TestForbiddenDisabledBlackOverlineWins(t *testing.T) {
	now := time.Now()
	g := gameWithStones(false, []Point{{7, 3}, {7, 4}, {7, 5}, {7, 6}, {7, 8}}, nil)
	g.NextColor = Black
	if _, err := ApplyMove(g, 7, 7, Human, now); err != nil {
		t.Fatalf("apply move: %v", err)
	}
	if g.Status != StatusBlackWon || g.EndReason != EndReasonFiveInRow {
		t.Fatalf("with 禁手 off, black overline should win: status=%s reason=%s", g.Status, g.EndReason)
	}
}

func TestForbiddenAllowsLegalBlackMoves(t *testing.T) {
	now := time.Now()
	// A single open three is legal; the move should simply continue the game.
	g := gameWithStones(true, []Point{{7, 5}, {7, 6}}, nil)
	g.NextColor = Black
	if _, err := ApplyMove(g, 7, 7, Human, now); err != nil {
		t.Fatalf("apply move: %v", err)
	}
	if g.Status != StatusPlaying || g.NextColor != White {
		t.Fatalf("single open three should be legal: status=%s next=%s", g.Status, g.NextColor)
	}
}

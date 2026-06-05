package game

import (
	"errors"
	"testing"
	"time"
)

func TestCheckWinDirections(t *testing.T) {
	tests := []struct {
		name   string
		points []Point
		last   Point
	}{
		{
			name:   "horizontal",
			points: []Point{{7, 3}, {7, 4}, {7, 5}, {7, 6}, {7, 7}},
			last:   Point{7, 7},
		},
		{
			name:   "vertical",
			points: []Point{{2, 9}, {3, 9}, {4, 9}, {5, 9}, {6, 9}},
			last:   Point{6, 9},
		},
		{
			name:   "diagonal_down",
			points: []Point{{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}},
			last:   Point{5, 5},
		},
		{
			name:   "diagonal_up",
			points: []Point{{8, 3}, {7, 4}, {6, 5}, {5, 6}, {4, 7}},
			last:   Point{4, 7},
		},
		{
			name:   "overline_counts_as_win",
			points: []Point{{4, 4}, {4, 5}, {4, 6}, {4, 7}, {4, 8}, {4, 9}},
			last:   Point{4, 9},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			board := BuildBoard(movesFromPoints(tt.points, Black))
			line := CheckWin(board, tt.last.Row, tt.last.Col, Black)
			if len(line) < 5 {
				t.Fatalf("expected a winning line, got %v", line)
			}
		})
	}
}

func TestApplyMoveValidations(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	g := Game{
		HumanColor: Black,
		AgentColor: White,
		NextColor:  Black,
		Status:     StatusPlaying,
	}

	if _, err := ApplyMove(&g, -1, 0, Human, now); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("expected out of bounds, got %v", err)
	}
	if _, err := ApplyMove(&g, 0, 0, Agent, now); !errors.Is(err, ErrWrongTurn) {
		t.Fatalf("expected wrong turn, got %v", err)
	}
	if _, err := ApplyMove(&g, 0, 0, Human, now); err != nil {
		t.Fatalf("first move failed: %v", err)
	}
	if _, err := ApplyMove(&g, 0, 0, Agent, now); !errors.Is(err, ErrCellOccupied) {
		t.Fatalf("expected occupied cell, got %v", err)
	}
}

func TestAgentAgentTurnMapping(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	g := Game{
		Mode:      ModeAgentAgent,
		NextColor: Black,
		Status:    StatusPlaying,
	}

	if g.NextPlayer() != AgentBlack {
		t.Fatalf("expected black agent to start, got %s", g.NextPlayer())
	}
	if _, err := ApplyMove(&g, 7, 7, AgentWhite, now); !errors.Is(err, ErrWrongTurn) {
		t.Fatalf("expected white agent wrong turn, got %v", err)
	}
	if _, err := ApplyMove(&g, 7, 7, AgentBlack, now); err != nil {
		t.Fatalf("black agent first move failed: %v", err)
	}
	if g.NextPlayer() != AgentWhite {
		t.Fatalf("expected white agent turn, got %s", g.NextPlayer())
	}
	if err := Resign(&g, AgentWhite, now.Add(time.Second)); err != nil {
		t.Fatalf("white agent resign failed: %v", err)
	}
	if g.WinnerColor != Black || g.WinnerPlayer() != AgentBlack {
		t.Fatalf("expected black agent to win by resignation, got winner=%s role=%s", g.WinnerColor, g.WinnerPlayer())
	}
}

func TestApplyMoveDraw(t *testing.T) {
	g := Game{
		HumanColor: Black,
		AgentColor: White,
		NextColor:  Black,
		Status:     StatusPlaying,
	}

	blackPoints := make([]Point, 0, 112)
	whitePoints := make([]Point, 0, 112)
	for row := 0; row < BoardSize; row++ {
		for col := 0; col < BoardSize; col++ {
			point := Point{Row: row, Col: col}
			if point == (Point{Row: BoardSize - 1, Col: BoardSize - 1}) {
				continue
			}
			if drawPatternColor(row, col) == Black {
				blackPoints = append(blackPoints, point)
			} else {
				whitePoints = append(whitePoints, point)
			}
		}
	}
	if len(blackPoints) != len(whitePoints) {
		t.Fatalf("draw fixture must leave balanced moves, got black=%d white=%d", len(blackPoints), len(whitePoints))
	}

	for index := range blackPoints {
		g.Moves = append(g.Moves, Move{
			MoveNumber: len(g.Moves) + 1,
			Row:        blackPoints[index].Row,
			Col:        blackPoints[index].Col,
			Color:      Black,
			Player:     Human,
		})
		g.Moves = append(g.Moves, Move{
			MoveNumber: len(g.Moves) + 1,
			Row:        whitePoints[index].Row,
			Col:        whitePoints[index].Col,
			Color:      White,
			Player:     Agent,
		})
	}

	g.MoveCount = len(g.Moves)
	g.NextColor = Black

	if _, err := ApplyMove(&g, BoardSize-1, BoardSize-1, Human, time.Now()); err != nil {
		t.Fatalf("final move failed: %v", err)
	}
	if g.Status != StatusDraw {
		t.Fatalf("expected draw, got %s", g.Status)
	}
}

func drawPatternColor(row int, col int) Color {
	if (row+2*col+3)%4 < 2 {
		return Black
	}
	return White
}

func movesFromPoints(points []Point, color Color) []Move {
	moves := make([]Move, 0, len(points))
	player := Human
	if color == White {
		player = Agent
	}
	for index, point := range points {
		moves = append(moves, Move{
			MoveNumber: index + 1,
			Row:        point.Row,
			Col:        point.Col,
			Color:      color,
			Player:     player,
		})
	}
	return moves
}

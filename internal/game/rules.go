package game

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const BoardSize = 15

type Mode string

const (
	ModeHumanAgent Mode = "human-agent"
	ModeAgentAgent Mode = "agent-agent"
)

type Color string

const (
	Empty Color = ""
	Black Color = "black"
	White Color = "white"
)

type Player string

const (
	Human      Player = "human"
	Agent      Player = "agent"
	AgentBlack Player = "agent_black"
	AgentWhite Player = "agent_white"
)

type Status string

const (
	StatusPlaying  Status = "playing"
	StatusDraw     Status = "draw"
	StatusBlackWon Status = "black_won"
	StatusWhiteWon Status = "white_won"
)

type EndReason string

const (
	EndReasonNone        EndReason = ""
	EndReasonFiveInRow   EndReason = "five_in_row"
	EndReasonDraw        EndReason = "draw"
	EndReasonResignation EndReason = "resignation"
)

var (
	ErrOutOfBounds   = errors.New("move is outside the board")
	ErrCellOccupied  = errors.New("cell is already occupied")
	ErrWrongTurn     = errors.New("it is not this player's turn")
	ErrGameOver      = errors.New("game is already finished")
	ErrUnknownPlayer = errors.New("unknown player")
)

type Point struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

type Move struct {
	MoveNumber int       `json:"moveNumber"`
	Row        int       `json:"row"`
	Col        int       `json:"col"`
	Color      Color     `json:"color"`
	Player     Player    `json:"player"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Game struct {
	ID                      string     `json:"gameId"`
	Mode                    Mode       `json:"mode"`
	HumanToken              string     `json:"-"`
	AgentToken              string     `json:"-"`
	AgentBlackToken         string     `json:"-"`
	AgentWhiteToken         string     `json:"-"`
	HumanColor              Color      `json:"humanColor"`
	AgentColor              Color      `json:"agentColor"`
	NextColor               Color      `json:"nextColor,omitempty"`
	Status                  Status     `json:"status"`
	EndReason               EndReason  `json:"endReason,omitempty"`
	WinnerColor             Color      `json:"winner,omitempty"`
	WinLine                 []Point    `json:"winLine,omitempty"`
	Moves                   []Move     `json:"moves,omitempty"`
	MoveCount               int        `json:"moveCount"`
	AgentJoinedAt           *time.Time `json:"-"`
	AgentLastSeenAt         *time.Time `json:"-"`
	AgentThinking           bool       `json:"-"`
	AgentThinkingSince      *time.Time `json:"-"`
	AgentBlackJoinedAt      *time.Time `json:"-"`
	AgentBlackLastSeenAt    *time.Time `json:"-"`
	AgentBlackThinking      bool       `json:"-"`
	AgentBlackThinkingSince *time.Time `json:"-"`
	AgentWhiteJoinedAt      *time.Time `json:"-"`
	AgentWhiteLastSeenAt    *time.Time `json:"-"`
	AgentWhiteThinking      bool       `json:"-"`
	AgentWhiteThinkingSince *time.Time `json:"-"`
	CreatedAt               time.Time  `json:"createdAt"`
	UpdatedAt               time.Time  `json:"updatedAt"`
}

func NormalizeMode(value string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case "", ModeHumanAgent:
		return ModeHumanAgent, nil
	case ModeAgentAgent:
		return ModeAgentAgent, nil
	default:
		return "", fmt.Errorf("unsupported game mode %q", value)
	}
}

func NormalizeColor(value string) (Color, error) {
	switch Color(strings.ToLower(strings.TrimSpace(value))) {
	case Black:
		return Black, nil
	case White:
		return White, nil
	default:
		return Empty, fmt.Errorf("unsupported color %q", value)
	}
}

func Opposite(color Color) Color {
	if color == Black {
		return White
	}
	return Black
}

func WonStatus(color Color) Status {
	if color == Black {
		return StatusBlackWon
	}
	return StatusWhiteWon
}

func (g Game) NormalizedMode() Mode {
	if g.Mode == "" {
		return ModeHumanAgent
	}
	return g.Mode
}

func (g Game) PlayerForColor(color Color) Player {
	if color == Empty {
		return ""
	}
	if g.NormalizedMode() == ModeAgentAgent {
		if color == Black {
			return AgentBlack
		}
		return AgentWhite
	}
	if g.HumanColor == color {
		return Human
	}
	return Agent
}

func (g Game) ColorForPlayer(player Player) Color {
	switch player {
	case Human:
		return g.HumanColor
	case Agent:
		return g.AgentColor
	case AgentBlack:
		return Black
	case AgentWhite:
		return White
	default:
		return Empty
	}
}

func (g Game) NextPlayer() Player {
	if g.Status != StatusPlaying || g.NextColor == Empty {
		return ""
	}
	return g.PlayerForColor(g.NextColor)
}

func (g Game) WinnerPlayer() Player {
	if g.WinnerColor == Empty {
		return ""
	}
	return g.PlayerForColor(g.WinnerColor)
}

func IsAgentPlayer(player Player) bool {
	return player == Agent || player == AgentBlack || player == AgentWhite
}

func BuildBoard(moves []Move) [][]Color {
	board := make([][]Color, BoardSize)
	for row := range board {
		board[row] = make([]Color, BoardSize)
	}

	for _, move := range moves {
		if move.Row >= 0 && move.Row < BoardSize && move.Col >= 0 && move.Col < BoardSize {
			board[move.Row][move.Col] = move.Color
		}
	}
	return board
}

func ApplyMove(g *Game, row int, col int, player Player, now time.Time) (Move, error) {
	if g.Status != StatusPlaying {
		return Move{}, ErrGameOver
	}
	if row < 0 || row >= BoardSize || col < 0 || col >= BoardSize {
		return Move{}, ErrOutOfBounds
	}
	if g.NextPlayer() != player {
		return Move{}, ErrWrongTurn
	}

	board := BuildBoard(g.Moves)
	if board[row][col] != Empty {
		return Move{}, ErrCellOccupied
	}

	move := Move{
		MoveNumber: len(g.Moves) + 1,
		Row:        row,
		Col:        col,
		Color:      g.NextColor,
		Player:     player,
		CreatedAt:  now,
	}
	g.Moves = append(g.Moves, move)
	g.MoveCount = len(g.Moves)
	board[row][col] = move.Color

	if line := CheckWin(board, row, col, move.Color); len(line) >= 5 {
		g.Status = WonStatus(move.Color)
		g.EndReason = EndReasonFiveInRow
		g.WinnerColor = move.Color
		g.WinLine = line
		g.NextColor = Empty
	} else if len(g.Moves) == BoardSize*BoardSize {
		g.Status = StatusDraw
		g.EndReason = EndReasonDraw
		g.NextColor = Empty
	} else {
		g.NextColor = Opposite(move.Color)
	}
	g.UpdatedAt = now

	return move, nil
}

func Resign(g *Game, player Player, now time.Time) error {
	if g.Status != StatusPlaying {
		return ErrGameOver
	}

	loser := g.ColorForPlayer(player)
	if loser == Empty {
		return ErrUnknownPlayer
	}
	winner := Opposite(loser)
	g.Status = WonStatus(winner)
	g.EndReason = EndReasonResignation
	g.WinnerColor = winner
	g.WinLine = []Point{}
	g.NextColor = Empty
	g.UpdatedAt = now
	return nil
}

func CheckWin(board [][]Color, row int, col int, color Color) []Point {
	if color == Empty {
		return nil
	}

	directions := [][2]int{
		{0, 1},
		{1, 0},
		{1, 1},
		{1, -1},
	}

	for _, direction := range directions {
		line := []Point{{Row: row, Col: col}}
		line = append(collectDirection(board, row, col, color, -direction[0], -direction[1]), line...)
		line = append(line, collectDirection(board, row, col, color, direction[0], direction[1])...)
		if len(line) >= 5 {
			return line
		}
	}

	return nil
}

func collectDirection(board [][]Color, row int, col int, color Color, rowStep int, colStep int) []Point {
	points := make([]Point, 0, 4)
	for {
		row += rowStep
		col += colStep
		if row < 0 || row >= BoardSize || col < 0 || col >= BoardSize {
			return points
		}
		if board[row][col] != color {
			return points
		}
		points = append(points, Point{Row: row, Col: col})
	}
}

func NewGameID() (string, error) {
	return randomHex(12)
}

func NewToken() (string, error) {
	return randomHex(24)
}

func randomHex(byteCount int) (string, error) {
	buffer := make([]byte, byteCount)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"wuziqi/internal/game"
	"wuziqi/internal/store"
)

func TestGameAPIFlow(t *testing.T) {
	handler, closeStore := newTestHandler(t)
	defer closeStore()

	createBody := bytes.NewBufferString(`{"humanColor":"black"}`)
	createResponse := requestJSON(t, handler, http.MethodPost, "/api/games", "", createBody)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	var created GameResponse
	decodeResponse(t, createResponse, &created)
	if created.HumanToken == "" || created.AgentToken == "" {
		t.Fatalf("expected tokens in create response: %+v", created)
	}
	if created.AgentState.Joined || created.AgentState.Thinking {
		t.Fatalf("agent should start disconnected: %+v", created.AgentState)
	}
	if created.NextTurn != game.Human {
		t.Fatalf("expected human to start, got %s", created.NextTurn)
	}

	gamePath := "/api/games/" + created.GameID
	readResponse := requestJSON(t, handler, http.MethodGet, gamePath, "", nil)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("read status = %d, body = %s", readResponse.Code, readResponse.Body.String())
	}
	var readBack GameResponse
	decodeResponse(t, readResponse, &readBack)
	if readBack.HumanToken != "" || readBack.AgentToken != "" {
		t.Fatalf("tokens leaked in read response: %+v", readBack)
	}

	humanMove := requestJSON(t, handler, http.MethodPost, gamePath+"/moves", created.HumanToken, bytes.NewBufferString(`{"row":7,"col":7}`))
	if humanMove.Code != http.StatusOK {
		t.Fatalf("human move status = %d, body = %s", humanMove.Code, humanMove.Body.String())
	}
	var afterHuman GameResponse
	decodeResponse(t, humanMove, &afterHuman)
	if afterHuman.NextTurn != game.Agent {
		t.Fatalf("expected agent turn, got %s", afterHuman.NextTurn)
	}

	agentMove := requestJSON(t, handler, http.MethodPost, gamePath+"/moves", created.AgentToken, bytes.NewBufferString(`{"row":7,"col":8}`))
	if agentMove.Code != http.StatusOK {
		t.Fatalf("agent move status = %d, body = %s", agentMove.Code, agentMove.Body.String())
	}

	badToken := requestJSON(t, handler, http.MethodPost, gamePath+"/moves", "bad-token", bytes.NewBufferString(`{"row":7,"col":9}`))
	if badToken.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, body = %s", badToken.Code, badToken.Body.String())
	}

	wrongTurn := requestJSON(t, handler, http.MethodPost, gamePath+"/moves", created.AgentToken, bytes.NewBufferString(`{"row":8,"col":8}`))
	if wrongTurn.Code != http.StatusConflict {
		t.Fatalf("wrong turn status = %d, body = %s", wrongTurn.Code, wrongTurn.Body.String())
	}
}

func TestAgentAgentAPIFlow(t *testing.T) {
	handler, closeStore := newTestHandler(t)
	defer closeStore()

	createBody := bytes.NewBufferString(`{"mode":"agent-agent"}`)
	createResponse := requestJSON(t, handler, http.MethodPost, "/api/games", "", createBody)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	var created GameResponse
	decodeResponse(t, createResponse, &created)
	if created.Mode != game.ModeAgentAgent {
		t.Fatalf("expected agent-agent mode, got %s", created.Mode)
	}
	if created.HumanToken != "" || created.AgentToken != "" {
		t.Fatalf("agent-agent should not return human/legacy agent tokens: %+v", created)
	}
	if created.AgentBlackToken == "" || created.AgentWhiteToken == "" || created.AgentBlackToken == created.AgentWhiteToken {
		t.Fatalf("expected distinct black/white agent tokens: %+v", created)
	}
	if created.NextTurn != game.AgentBlack {
		t.Fatalf("expected black agent to start, got %s", created.NextTurn)
	}
	if len(created.AgentStates) != 2 || created.AgentStates[game.AgentBlack].Joined || created.AgentStates[game.AgentWhite].Joined {
		t.Fatalf("unexpected initial agent states: %+v", created.AgentStates)
	}

	gamePath := "/api/games/" + created.GameID
	blackJoin := requestJSON(t, handler, http.MethodPost, gamePath+"/agent/join", created.AgentBlackToken, nil)
	if blackJoin.Code != http.StatusOK {
		t.Fatalf("black join status = %d, body = %s", blackJoin.Code, blackJoin.Body.String())
	}
	whiteJoin := requestJSON(t, handler, http.MethodPost, gamePath+"/agent/join", created.AgentWhiteToken, nil)
	if whiteJoin.Code != http.StatusOK {
		t.Fatalf("white join status = %d, body = %s", whiteJoin.Code, whiteJoin.Body.String())
	}

	thinking := requestJSON(t, handler, http.MethodPost, gamePath+"/agent/status", created.AgentBlackToken, bytes.NewBufferString(`{"thinking":true}`))
	if thinking.Code != http.StatusOK {
		t.Fatalf("black thinking status = %d, body = %s", thinking.Code, thinking.Body.String())
	}
	var afterThinking GameResponse
	decodeResponse(t, thinking, &afterThinking)
	if !afterThinking.AgentStates[game.AgentBlack].Thinking || afterThinking.AgentStates[game.AgentWhite].Thinking {
		t.Fatalf("unexpected thinking states: %+v", afterThinking.AgentStates)
	}

	wrongTurn := requestJSON(t, handler, http.MethodPost, gamePath+"/moves", created.AgentWhiteToken, bytes.NewBufferString(`{"row":7,"col":7}`))
	if wrongTurn.Code != http.StatusConflict {
		t.Fatalf("wrong turn status = %d, body = %s", wrongTurn.Code, wrongTurn.Body.String())
	}

	blackMove := requestJSON(t, handler, http.MethodPost, gamePath+"/moves", created.AgentBlackToken, bytes.NewBufferString(`{"row":7,"col":7}`))
	if blackMove.Code != http.StatusOK {
		t.Fatalf("black move status = %d, body = %s", blackMove.Code, blackMove.Body.String())
	}
	var afterBlack GameResponse
	decodeResponse(t, blackMove, &afterBlack)
	if afterBlack.NextTurn != game.AgentWhite || afterBlack.Moves[0].Player != game.AgentBlack {
		t.Fatalf("unexpected state after black move: %+v", afterBlack)
	}
	if afterBlack.AgentStates[game.AgentBlack].Thinking {
		t.Fatalf("black move should clear black thinking: %+v", afterBlack.AgentStates)
	}

	whiteMove := requestJSON(t, handler, http.MethodPost, gamePath+"/moves", created.AgentWhiteToken, bytes.NewBufferString(`{"row":7,"col":8}`))
	if whiteMove.Code != http.StatusOK {
		t.Fatalf("white move status = %d, body = %s", whiteMove.Code, whiteMove.Body.String())
	}
	var afterWhite GameResponse
	decodeResponse(t, whiteMove, &afterWhite)
	if afterWhite.NextTurn != game.AgentBlack || afterWhite.Moves[1].Player != game.AgentWhite {
		t.Fatalf("unexpected state after white move: %+v", afterWhite)
	}

	resign := requestJSON(t, handler, http.MethodPost, gamePath+"/resign", created.AgentBlackToken, nil)
	if resign.Code != http.StatusOK {
		t.Fatalf("black resign status = %d, body = %s", resign.Code, resign.Body.String())
	}
	var afterResign GameResponse
	decodeResponse(t, resign, &afterResign)
	if afterResign.Status != game.StatusWhiteWon || afterResign.WinnerRole != game.AgentWhite || afterResign.ResignedBy != game.AgentBlack {
		t.Fatalf("unexpected black resign result: %+v", afterResign)
	}
}

func TestAgentStateAPI(t *testing.T) {
	handler, closeStore := newTestHandler(t)
	defer closeStore()

	createResponse := requestJSON(t, handler, http.MethodPost, "/api/games", "", bytes.NewBufferString(`{"humanColor":"black"}`))
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var created GameResponse
	decodeResponse(t, createResponse, &created)
	gamePath := "/api/games/" + created.GameID

	badJoin := requestJSON(t, handler, http.MethodPost, gamePath+"/agent/join", created.HumanToken, nil)
	if badJoin.Code != http.StatusUnauthorized {
		t.Fatalf("human token join status = %d, body = %s", badJoin.Code, badJoin.Body.String())
	}

	join := requestJSON(t, handler, http.MethodPost, gamePath+"/agent/join", created.AgentToken, nil)
	if join.Code != http.StatusOK {
		t.Fatalf("join status = %d, body = %s", join.Code, join.Body.String())
	}
	var afterJoin GameResponse
	decodeResponse(t, join, &afterJoin)
	if !afterJoin.AgentState.Joined || afterJoin.AgentState.Thinking || afterJoin.AgentState.LastSeenAt == nil {
		t.Fatalf("unexpected join state: %+v", afterJoin.AgentState)
	}

	thinking := requestJSON(t, handler, http.MethodPost, gamePath+"/agent/status", created.AgentToken, bytes.NewBufferString(`{"thinking":true}`))
	if thinking.Code != http.StatusOK {
		t.Fatalf("thinking status = %d, body = %s", thinking.Code, thinking.Body.String())
	}
	var afterThinking GameResponse
	decodeResponse(t, thinking, &afterThinking)
	if !afterThinking.AgentState.Joined || !afterThinking.AgentState.Thinking || afterThinking.AgentState.ThinkingSince == nil {
		t.Fatalf("unexpected thinking state: %+v", afterThinking.AgentState)
	}

	humanMove := requestJSON(t, handler, http.MethodPost, gamePath+"/moves", created.HumanToken, bytes.NewBufferString(`{"row":7,"col":7}`))
	if humanMove.Code != http.StatusOK {
		t.Fatalf("human move status = %d, body = %s", humanMove.Code, humanMove.Body.String())
	}
	agentMove := requestJSON(t, handler, http.MethodPost, gamePath+"/moves", created.AgentToken, bytes.NewBufferString(`{"row":7,"col":8}`))
	if agentMove.Code != http.StatusOK {
		t.Fatalf("agent move status = %d, body = %s", agentMove.Code, agentMove.Body.String())
	}
	var afterMove GameResponse
	decodeResponse(t, agentMove, &afterMove)
	if !afterMove.AgentState.Joined || afterMove.AgentState.Thinking || afterMove.AgentState.ThinkingSince != nil {
		t.Fatalf("agent move should clear thinking: %+v", afterMove.AgentState)
	}
}

func TestResignAPI(t *testing.T) {
	handler, closeStore := newTestHandler(t)
	defer closeStore()

	createResponse := requestJSON(t, handler, http.MethodPost, "/api/games", "", bytes.NewBufferString(`{"humanColor":"black"}`))
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var created GameResponse
	decodeResponse(t, createResponse, &created)
	gamePath := "/api/games/" + created.GameID

	resign := requestJSON(t, handler, http.MethodPost, gamePath+"/resign", created.HumanToken, nil)
	if resign.Code != http.StatusOK {
		t.Fatalf("human resign status = %d, body = %s", resign.Code, resign.Body.String())
	}
	var afterResign GameResponse
	decodeResponse(t, resign, &afterResign)
	if afterResign.Status != game.StatusWhiteWon || afterResign.EndReason != game.EndReasonResignation {
		t.Fatalf("unexpected resign result: %+v", afterResign)
	}
	if afterResign.Winner != game.White || afterResign.WinnerRole != game.Agent || afterResign.ResignedBy != game.Human {
		t.Fatalf("unexpected winner/resigner: %+v", afterResign)
	}

	moveAfterResign := requestJSON(t, handler, http.MethodPost, gamePath+"/moves", created.AgentToken, bytes.NewBufferString(`{"row":7,"col":7}`))
	if moveAfterResign.Code != http.StatusConflict {
		t.Fatalf("move after resign status = %d, body = %s", moveAfterResign.Code, moveAfterResign.Body.String())
	}
}

func TestAgentResignClearsThinking(t *testing.T) {
	handler, closeStore := newTestHandler(t)
	defer closeStore()

	createResponse := requestJSON(t, handler, http.MethodPost, "/api/games", "", bytes.NewBufferString(`{"humanColor":"black"}`))
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var created GameResponse
	decodeResponse(t, createResponse, &created)
	gamePath := "/api/games/" + created.GameID

	thinking := requestJSON(t, handler, http.MethodPost, gamePath+"/agent/status", created.AgentToken, bytes.NewBufferString(`{"thinking":true}`))
	if thinking.Code != http.StatusOK {
		t.Fatalf("thinking status = %d, body = %s", thinking.Code, thinking.Body.String())
	}

	resign := requestJSON(t, handler, http.MethodPost, gamePath+"/resign", created.AgentToken, nil)
	if resign.Code != http.StatusOK {
		t.Fatalf("agent resign status = %d, body = %s", resign.Code, resign.Body.String())
	}
	var afterResign GameResponse
	decodeResponse(t, resign, &afterResign)
	if afterResign.Status != game.StatusBlackWon || afterResign.EndReason != game.EndReasonResignation {
		t.Fatalf("unexpected agent resign result: %+v", afterResign)
	}
	if afterResign.WinnerRole != game.Human || afterResign.ResignedBy != game.Agent {
		t.Fatalf("unexpected agent resign roles: %+v", afterResign)
	}
	if !afterResign.AgentState.Joined || afterResign.AgentState.Thinking || afterResign.AgentState.ThinkingSince != nil {
		t.Fatalf("agent resign should clear thinking: %+v", afterResign.AgentState)
	}
}

func TestListGames(t *testing.T) {
	handler, closeStore := newTestHandler(t)
	defer closeStore()

	for i := 0; i < 2; i++ {
		response := requestJSON(t, handler, http.MethodPost, "/api/games", "", bytes.NewBufferString(`{"humanColor":"white"}`))
		if response.Code != http.StatusCreated {
			t.Fatalf("create status = %d", response.Code)
		}
	}

	response := requestJSON(t, handler, http.MethodGet, "/api/games?limit=20", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.Code, response.Body.String())
	}
	var games []GameListItem
	decodeResponse(t, response, &games)
	if len(games) != 2 {
		t.Fatalf("expected 2 games, got %d", len(games))
	}
}

func newTestHandler(t *testing.T) (http.Handler, func()) {
	t.Helper()
	sqliteStore, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "wuziqi.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return New(sqliteStore, "").Handler(), func() {
		_ = sqliteStore.Close()
	}
}

func requestJSON(t *testing.T, handler http.Handler, method string, path string, token string, body *bytes.Buffer) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body.Bytes())
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

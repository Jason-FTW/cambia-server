package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jason-s-yu/cambia/game"
	"github.com/jason-s-yu/cambia/models"
)

type GameServer struct {
	GameStore *game.GameStore
	Logf      func(f string, v ...interface{})
}

func NewGameServer() *GameServer {
	gs := &GameServer{
		GameStore: game.NewGameStore(),
		Logf:      log.Printf,
	}

	return gs
}

func (s GameServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/game/create" {
		s.CreateGameHandler(w, r)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"game"}})
	if err != nil {
		s.Logf("%v", err)
		return
	}

	if c.Subprotocol() != "game" {
		c.Close(websocket.StatusPolicyViolation, "client must speak the game subprotocol")
		return
	}

	// ensure a game ID is supplied in the path
	queryGameID := r.URL.Path[len("/game/"):]
	if queryGameID == "" {
		s.Logf("missing game_id")
		c.Close(websocket.StatusPolicyViolation, "missing game_id")
		return
	}

	// convert ID string to UUID
	gameID, err := uuid.Parse(queryGameID)
	if err != nil {
		s.Logf("invalid game_id: %v", err)
		c.Close(websocket.StatusPolicyViolation, "invalid uuid game_id")
		return
	}

	// check if the game exists
	game, ok := s.GameStore.GetGame(gameID)
	if !ok {
		s.Logf("game_id not found: %v", gameID)
		c.Close(websocket.StatusPolicyViolation, "game_id not found")
		return
	}

	player := &models.Player{
		ID:        gameID,
		Conn:      c,
		Connected: true,
	}

	// Store the player in the game instance
	game.Mutex.Lock()
	game.Players = append(game.Players, player)
	game.Mutex.Unlock()
}

func (s *GameServer) CreateGameHandler(w http.ResponseWriter, r *http.Request) {
	g := game.NewGame()

	w.Header().Set("Content-Type", "application/json")

	s.GameStore.AddGame(g)

	if err := json.NewEncoder(w).Encode(g); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

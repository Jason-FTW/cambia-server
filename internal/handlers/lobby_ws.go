// internal/handlers/lobby_ws.go

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jason-s-yu/cambia/internal/auth"
	"github.com/jason-s-yu/cambia/internal/database"
	"github.com/jason-s-yu/cambia/internal/lobby"
	"github.com/sirupsen/logrus"
)

// LobbyWSHandler returns an http.HandlerFunc that upgrades to a WebSocket
// for the given lobby, subprotocol "lobby". It uses a LobbyManager to track real-time state.
// LobbyWSHandler handles WebSocket connections for a lobby.
// It performs the following steps:
// 1. Parses {lobby_id} from the request path.
// 2. Checks if the subprotocol is "lobby".
// 3. Authenticates the user using the auth_token from the cookie.
// 4. Verifies if the user is a participant in the specified lobby.
// 5. Accepts the WebSocket connection, tracks it in the LobbyManager, and starts the read loop.
//
// Parameters:
// - logger: A logrus.Logger instance for logging.
// - lm: A LobbyManager instance to manage lobby states.
//
// Returns:
// - An http.HandlerFunc that handles the WebSocket connection.
func LobbyWSHandler(logger *logrus.Logger, lm *lobby.LobbyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/lobby/ws/"), "/")
		if len(pathParts) < 1 {
			http.Error(w, "missing lobby_id", http.StatusBadRequest)
			return
		}
		lobbyIDStr := pathParts[0]
		lobbyUUID, err := uuid.Parse(lobbyIDStr)
		if err != nil {
			http.Error(w, "invalid lobby_id", http.StatusBadRequest)
			return
		}

		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:   []string{"lobby"},
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			logger.Warnf("websocket accept error: %v", err)
			return
		}
		if c.Subprotocol() != "lobby" {
			c.Close(websocket.StatusPolicyViolation, "client must speak the lobby subprotocol")
			return
		}

		// Auth
		token := extractCookieToken(r.Header.Get("Cookie"), "auth_token")
		userIDStr, err := auth.AuthenticateJWT(token)
		if err != nil {
			logger.Warnf("invalid token: %v", err)
			c.Close(websocket.StatusPolicyViolation, "invalid auth_token")
			return
		}
		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			logger.Warnf("invalid userID parse: %v", err)
			c.Close(websocket.StatusPolicyViolation, "invalid user ID")
			return
		}

		// Check DB: is user in that lobby?
		inLobby, dbErr := database.IsUserInLobby(r.Context(), lobbyUUID, userID)
		if dbErr != nil {
			logger.Warnf("db error checking membership: %v", dbErr)
			c.Close(websocket.StatusInternalError, "db error")
			return
		}
		if !inLobby {
			logger.Warnf("user not in lobby")
			c.Close(websocket.StatusPolicyViolation, "user not in that lobby")
			return
		}

		// If ok, register user in LobbyManager
		ls := lm.GetOrCreateLobbyState(lobbyUUID)
		ctx, cancel := context.WithCancel(r.Context())
		conn := &lobby.LobbyConnection{
			UserID:  userID,
			Cancel:  cancel,
			OutChan: make(chan map[string]interface{}, 10),
		}
		ls.Connections[userID] = conn
		ls.ReadyStates[userID] = false // by default, user is not ready
		logger.Infof("User %v connected to lobby %v", userID, lobbyUUID)

		// Start a goroutine to write messages from OutChan to the websocket
		go writePump(ctx, c, conn, logger)

		// Broadcast that a new user joined
		ls.BroadcastJoin(userID)

		// read loop
		readPump(ctx, c, ls, conn, logger)
	}
}

// readPump reads messages from the websocket until disconnect. We handle JSON commands here.
func readPump(ctx context.Context, c *websocket.Conn, ls *lobby.LobbyState, conn *lobby.LobbyConnection, logger *logrus.Logger) {
	defer func() {
		// on exit
		ls.RemoveUser(conn.UserID)
		conn.Cancel()
		c.Close(websocket.StatusNormalClosure, "closing")
	}()

	for {
		typ, msg, err := c.Read(ctx)
		if err != nil {
			logger.Infof("user %v read err: %v", conn.UserID, err)
			return
		}
		if typ != websocket.MessageText {
			continue
		}

		var packet map[string]interface{}
		if err := json.Unmarshal(msg, &packet); err != nil {
			logger.Warnf("invalid json from user %v: %v", conn.UserID, err)
			continue
		}

		handleLobbyMessage(packet, ls, conn, logger)
	}
}

// handleLobbyMessage interprets the "type" field received by client and updates the lobby or broadcasts accordingly.
func handleLobbyMessage(packet map[string]interface{}, ls *lobby.LobbyState, conn *lobby.LobbyConnection, logger *logrus.Logger) {
	action, _ := packet["type"].(string)
	switch action {
	case "ready":
		ls.ReadyStates[conn.UserID] = true
		ls.BroadcastReadyState(conn.UserID, true)
		// If autoStart is on and everyone is ready, start countdown
		if ls.AutoStart && ls.AreAllReady() {
			ls.StartCountdown(10)
		}
	case "unready":
		ls.ReadyStates[conn.UserID] = false
		ls.BroadcastReadyState(conn.UserID, false)
		// Cancel countdown if any
		ls.CancelCountdown()
	case "leave_lobby":
		// remove from DB's lobby_participants
		err := database.RemoveUserFromLobby(context.Background(), conn.UserID, ls.LobbyID)
		if err != nil {
			logger.Warnf("failed to remove user %v from DB: %v", conn.UserID, err)
		}
		ls.BroadcastLeave(conn.UserID)
		// we can close the socket
		conn.Cancel()
	case "chat":
		msg, _ := packet["msg"].(string)
		ls.BroadcastChat(conn.UserID, msg)
	default:
		logger.Warnf("unknown action %s from user %v", action, conn.UserID)
	}
}

// writePump writes messages from conn.OutChan to the websocket until context is canceled.
func writePump(ctx context.Context, c *websocket.Conn, conn *lobby.LobbyConnection, logger *logrus.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-conn.OutChan:
			data, err := json.Marshal(msg)
			if err != nil {
				logger.Warnf("failed to marshal out msg: %v", err)
				continue
			}
			err = c.Write(ctx, websocket.MessageText, data)
			if err != nil {
				logger.Warnf("failed to write to ws: %v", err)
				return
			}
		}
	}
}

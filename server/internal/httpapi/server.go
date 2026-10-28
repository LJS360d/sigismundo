// Package httpapi is the API behind the Activity (activity/, which proxies /api
// here): the OAuth2 token exchange and the WebSocket carrying state, input
// and the stream.
package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"sigismundo/internal/catalog"
	"sigismundo/internal/config"
	"sigismundo/internal/logger"
	"sigismundo/internal/session"
)

type Server struct {
	appID    string
	devAuth  bool
	sessions *session.Manager
}

// NewServer serves the Activity of application appID. devAuth enables
// /api/dev-token, which hands out tokens for any user id.
func NewServer(appID string, devAuth bool, sessions *session.Manager) *Server {
	return &Server{appID: appID, devAuth: devAuth, sessions: sessions}
}

func (s *Server) Start() {
	srv := &http.Server{
		Addr:              config.ServerAddress,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	logger.Infof("Server listening at %v", config.ServerAddress)
	logger.Fatalf("http server: %v", srv.ListenAndServe())
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/token", jsonOnly(s.token))
	if s.devAuth {
		logger.Warnf("DEV_AUTH is on: /api/dev-token hands out tokens for any user id")
		mux.HandleFunc("GET /api/dev-token", s.devToken)
	}
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"client_id": s.appID})
	})
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, append([]catalog.Entry{}, s.sessions.Catalog.Entries...))
	})
	mux.HandleFunc("GET /api/catalog/{id}/image", func(w http.ResponseWriter, r *http.Request) {
		e, ok := s.sessions.Catalog.Get(r.PathValue("id"))
		if !ok || e.Image == "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, e.Image)
	})
	mux.HandleFunc("GET /api/ws", s.ws)
	return mux
}

// jsonOnly rejects requests without a JSON body type, so a cross-site form
// can't trigger them (it would need a CORS preflight).
func jsonOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "content-type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Errorf("write json: %v", err)
	}
}

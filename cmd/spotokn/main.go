package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/botxlab/spotokn/internal"
)

const (
	defaultPort         = 8080
	readTimeout         = 10 * time.Second
	writeTimeout        = 10 * time.Second
	idleTimeout         = 120 * time.Second
	errMethodNotAllowed = "Method not allowed"
)

type Server struct {
	httpServer   *http.Server
	tokenService *internal.TokenService
	logger       *internal.Logger
}

func NewServer() *Server {
	logger := internal.NewLogger()
	tokenService := internal.NewTokenService(logger)

	mux := http.NewServeMux()
	server := &Server{
		tokenService: tokenService,
		logger:       logger,
	}

	mux.HandleFunc("/api/token", server.handleToken)
	mux.HandleFunc("/api/query", server.handleHash)
	mux.HandleFunc("/api/metadata", server.handleMetaData)
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/", server.handleNotFound)

	port := getEnvInt("PORT", defaultPort)
	server.httpServer = &http.Server{
		Addr:         ":" + strconv.Itoa(port),
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	return server
}

// GET /api/query?playlist=spotify:playlist:ID
func (s *Server) handleHash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.respondError(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := internal.GetSpotifyQueryResultFromRequest(ctx, r)
	if err != nil {
		s.logger.Error("Hash fetch failed: " + err.Error())
		s.respondError(w, http.StatusServiceUnavailable, "Could not get hash")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}
func (s *Server) handleMetaData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.respondError(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	res, err := internal.GetMetadataFromRequest(ctx, r)
	if err != nil {
		s.logger.Error("Metadata fetch failed: " + err.Error())
		s.respondError(w, http.StatusServiceUnavailable, "Could not get metadata")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(res)
}
func (s *Server) Start() error {
	port := s.httpServer.Addr
	s.logger.Info("Spotify Token Service started on port" + port)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down gracefully...")
	s.tokenService.Cleanup()
	err := s.httpServer.Shutdown(ctx)
	if err == nil {
		s.logger.Info("Shutdown complete")
	}
	return err
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.respondError(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}

	query := r.URL.Query()
	debug := query.Get("debug") == "true"

	if debug {
		status := s.tokenService.GetStatus()
		s.respondJSON(w, http.StatusOK, status)
		return
	}

	cookies := parseCookies(r)
	token, err := s.tokenService.GetToken(cookies)

	if err != nil || token == nil {
		s.logger.Error("Token fetch failed: " + err.Error())
		s.respondError(w, http.StatusServiceUnavailable, "Service temporarily unavailable")
		return
	}

	s.respondJSON(w, http.StatusOK, token)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.respondError(w, http.StatusMethodNotAllowed, errMethodNotAllowed)
		return
	}

	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UnixMilli(),
		"service":   "spotokn",
	}
	s.respondJSON(w, http.StatusOK, health)
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.respondError(w, http.StatusNotFound, "Endpoint not found")
}

func (s *Server) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) respondError(w http.ResponseWriter, status int, message string) {
	s.respondJSON(w, status, map[string]interface{}{
		"success":   false,
		"error":     message,
		"timestamp": time.Now().UnixMilli(),
	})
}

func parseCookies(r *http.Request) []internal.Cookie {
	cookies := []internal.Cookie{}
	for _, c := range r.Cookies() {
		cookies = append(cookies, internal.Cookie{
			Name:  c.Name,
			Value: c.Value,
		})
	}
	return cookies
}

func getEnvInt(key string, defaultValue int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultValue
}

func main() {
	server := NewServer()

	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			server.logger.Error("Server error: " + err.Error())
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		server.logger.Error("Shutdown error: " + err.Error())
		os.Exit(1)
	}
}

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"ride-home-router/internal/server"
)

// App struct holds the Wails application state
type App struct {
	ctx    context.Context
	server *server.Server
	url    string
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Start the HTTP server on a random available port
	srv, err := server.New(server.Config{
		Addr: "127.0.0.1:0", // 0 = random available port
	})
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	addr, err := srv.Start()
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	a.server = srv
	a.url = fmt.Sprintf("http://%s", addr)

	log.Printf("Internal HTTP server running at %s", a.url)

	// Navigate the WebView to the internal server after a brief delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		runtime.WindowExecJS(ctx, fmt.Sprintf(`window.location.href = "%s"`, a.url))
	}()
}

// shutdown is called when the app closes
func (a *App) shutdown(ctx context.Context) {
	if a.server != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := a.server.Shutdown(shutdownCtx); err != nil {
			log.Printf("Error shutting down server: %v", err)
		}
	}
}

// GetServerURL returns the internal server URL (for debugging)
func (a *App) GetServerURL() string {
	return a.url
}

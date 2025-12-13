package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"ride-home-router/internal/server"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("Fatal error: %v", err)
	}
}

func run() error {
	addr := getEnv("SERVER_ADDR", "127.0.0.1:8080")

	srv, err := server.New(server.Config{
		Addr: addr,
	})
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	actualAddr, err := srv.Start()
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// Open browser after a short delay to ensure server is ready
	go func() {
		time.Sleep(500 * time.Millisecond)
		url := fmt.Sprintf("http://%s", actualAddr)
		if err := openBrowser(url); err != nil {
			log.Printf("Could not open browser: %v", err)
		} else {
			log.Printf("Opened browser at %s", url)
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	sig := <-shutdown
	log.Printf("Received signal %v, starting graceful shutdown", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("could not gracefully shutdown the server: %w", err)
	}

	log.Println("Server stopped")
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default: // linux, freebsd, etc.
		cmd = exec.Command("xdg-open", url)
	}

	return cmd.Start()
}

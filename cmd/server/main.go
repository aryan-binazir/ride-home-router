package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"regexp"
	"ride-home-router/internal/server"
	"strings"
	"syscall"
	"time"
)

const (
	defaultPort           = "8080"
	serverShutdownTimeout = 30 * time.Second
)

type options struct {
	Addr             string
	AllowedHosts     []string
	DatabaseURL      string
	GoogleMapsAPIKey string
}

type applicationServer interface {
	Start() (string, error)
	Errors() <-chan error
	Shutdown(context.Context) error
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("Fatal error: %v", err)
	}
}

func run(args []string) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(shutdown)

	srv, err := server.New(context.Background(), server.Config{
		Addr:             opts.Addr,
		AllowedHosts:     opts.AllowedHosts,
		DatabaseURL:      opts.DatabaseURL,
		GoogleMapsAPIKey: opts.GoogleMapsAPIKey,
	})
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	return serve(srv, shutdown)
}

func serve(srv applicationServer, shutdown <-chan os.Signal) error {
	actualAddr, err := srv.Start()
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	log.Printf("Ride Home Router listening on http://%s", actualAddr)

	var serveErr error
	select {
	case sig := <-shutdown:
		log.Printf("Received signal %v, starting graceful shutdown", sig)
	case err := <-srv.Errors():
		serveErr = fmt.Errorf("server stopped unexpectedly: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
	defer cancel()

	shutdownErr := srv.Shutdown(ctx)
	if serveErr == nil {
		select {
		case err := <-srv.Errors():
			serveErr = fmt.Errorf("server stopped unexpectedly: %w", err)
		default:
		}
	}
	if shutdownErr != nil {
		shutdownErr = fmt.Errorf("could not gracefully shutdown the server: %w", shutdownErr)
	}
	if err := errors.Join(serveErr, shutdownErr); err != nil {
		return err
	}

	log.Println("Server stopped")
	return nil
}

// parseArgs rejects public listeners without an explicit host allowlist.
func parseArgs(args []string) (options, error) {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	addr := flags.String("addr", "", "listen address (default 127.0.0.1:$PORT, falling back to 127.0.0.1:"+defaultPort+")")
	allowedHosts := flags.String("allowed-hosts", "", "comma-separated public hostnames forwarded in Host/Origin by the tunnel or proxy in front of this server")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, errors.New("usage: server [--addr address] [--allowed-hosts host,...]")
	}

	opts := options{
		Addr:             *addr,
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		GoogleMapsAPIKey: os.Getenv("GOOGLE_MAPS_API_KEY"),
	}
	if opts.DatabaseURL == "" {
		return options{}, errors.New("DATABASE_URL is required (Postgres connection string)")
	}
	if opts.Addr == "" {
		port := os.Getenv("PORT")
		if port == "" {
			port = defaultPort
		}
		opts.Addr = net.JoinHostPort("127.0.0.1", port)
	}
	for host := range strings.SplitSeq(*allowedHosts, ",") {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if !validAllowedHost(host) {
			return options{}, fmt.Errorf("--allowed-hosts entry %q must be a bare hostname or IP without scheme, port, or path; the listener port and the scheme default are matched automatically", host)
		}
		opts.AllowedHosts = append(opts.AllowedHosts, host)
	}

	host, _, err := net.SplitHostPort(opts.Addr)
	if err != nil {
		return options{}, fmt.Errorf("invalid --addr %q: %w", opts.Addr, err)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if !loopback && len(opts.AllowedHosts) == 0 {
		return options{}, fmt.Errorf(
			"refusing to bind unauthenticated server to non-loopback address %q without --allowed-hosts naming the public hostname(s) it is reached by",
			opts.Addr,
		)
	}
	return opts, nil
}

var hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$`)

// validAllowedHost accepts a DNS name or IP without a port.
func validAllowedHost(host string) bool {
	if hostnamePattern.MatchString(host) {
		return true
	}
	if inner, ok := strings.CutPrefix(host, "["); ok {
		inner, ok = strings.CutSuffix(inner, "]")
		return ok && net.ParseIP(inner) != nil && strings.Contains(inner, ":")
	}
	return net.ParseIP(host) != nil && !strings.Contains(host, ":")
}

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

	// Installed before migrations run so a SIGTERM during startup is not the
	// default handler killing the process mid-migration.
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	srv, err := server.New(context.Background(), server.Config{
		Addr:             opts.Addr,
		AllowedHosts:     opts.AllowedHosts,
		DatabaseURL:      opts.DatabaseURL,
		GoogleMapsAPIKey: opts.GoogleMapsAPIKey,
	})
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	actualAddr, err := srv.Start()
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	log.Printf("Ride Home Router listening on http://%s", actualAddr)

	sig := <-shutdown
	log.Printf("Received signal %v, starting graceful shutdown", sig)

	ctx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("could not gracefully shutdown the server: %w", err)
	}

	log.Println("Server stopped")
	return nil
}

// parseArgs resolves the listen address, the public hostnames this server may
// be addressed by, and the environment-provided secrets. The address defaults
// to loopback on $PORT (or 8080); binding anywhere else requires
// --allowed-hosts because the server has no authentication of its own and
// relies on a tunnel or proxy in front of it. DATABASE_URL is required;
// GOOGLE_MAPS_API_KEY is optional.
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

// validAllowedHost accepts a DNS hostname or an IP literal, exactly as a
// browser would send it in Host without a port.
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

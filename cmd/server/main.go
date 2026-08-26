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
	Addr         string
	AllowedHosts []string
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

	srv, err := server.New(server.Config{
		Addr:         opts.Addr,
		AllowedHosts: opts.AllowedHosts,
	})
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	actualAddr, err := srv.Start()
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	log.Printf("Ride Home Router listening on http://%s", actualAddr)

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

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

// parseArgs resolves the listen address and the public hostnames this server
// may be addressed by. The address defaults to loopback on $PORT (or 8080);
// binding anywhere else requires --allowed-hosts because the server has no
// authentication of its own and relies on a tunnel or proxy in front of it.
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

	opts := options{Addr: *addr}
	if opts.Addr == "" {
		port := os.Getenv("PORT")
		if port == "" {
			port = defaultPort
		}
		opts.Addr = net.JoinHostPort("127.0.0.1", port)
	}
	for host := range strings.SplitSeq(*allowedHosts, ",") {
		if host = strings.TrimSpace(host); host != "" {
			opts.AllowedHosts = append(opts.AllowedHosts, host)
		}
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

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/server"
	"ride-home-router/migrations"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed seed.sql
var seedSQL string

type configuration struct {
	DatabaseURL   string `json:"database_url"`
	EncryptionKey string `json:"encryption_key"`
	Port          int    `json:"port"`
	Capability    string `json:"capability"`
	Cookie        string `json:"cookie"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	state := flag.String("state", "", "private lifecycle state directory")
	flag.Parse()
	data, err := os.ReadFile(filepath.Join(*state, "config.json"))
	if err != nil {
		return err
	}
	var cfg configuration
	if err = json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err = migrations.Run(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	var seeded bool
	if err = conn.QueryRow(ctx, "SELECT to_regclass('local_dev_seed') IS NOT NULL").Scan(&seeded); err != nil {
		return err
	}
	if seeded {
		if err = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM local_dev_seed)").Scan(&seeded); err != nil {
			return err
		}
	}
	db, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	if err = db.ConfigureCredentialEncryption(cfg.EncryptionKey); err != nil {
		_ = db.Close()
		return err
	}
	if !seeded {
		err = db.Settings().SetGoogleMapsKey(ctx, "synthetic-local-only")
	}
	_ = db.Close()
	if err != nil {
		return err
	}
	if _, err = conn.Exec(ctx, seedSQL); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	origin := "http://" + listener.Addr().String()
	p, err := newProvider(origin)
	if err != nil {
		return err
	}
	p.capability = cfg.Capability
	providerListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	providerHost := providerListener.Addr().String()
	providerServer := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != providerHost || r.Header.Get("Origin") != "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if r.Header.Get("X-Local-Provider") == "api.clerk.com" {
			p.api(w, r)
		} else {
			google(w, r)
		}
	})}
	defer func() { _ = providerServer.Close() }()
	go func() {
		if err := providerServer.Serve(providerListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("provider: %v", err)
			cancel()
		}
	}()
	transport := localTransport{endpoint: providerHost, base: &http.Transport{Proxy: nil}}
	http.DefaultTransport = transport
	srv, err := server.New(ctx, server.Config{DatabaseURL: cfg.DatabaseURL, CredentialEncryptionKey: cfg.EncryptionKey, Auth: p.config(&http.Client{Transport: transport, Timeout: 5 * time.Second}), Addr: strings.Replace(listener.Addr().String(), "127.0.0.1:", "127.0.0.2:", 1)})
	if err != nil {
		return err
	}
	backend, err := srv.Start()
	if err != nil {
		return err
	}
	defer func() {
		shutdown, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		_ = srv.Shutdown(shutdown)
	}()
	target, _ := url.Parse("http://" + backend)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{Proxy: nil}
	public := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != listener.Addr().String() {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		cookies := r.Cookies()
		r.Header.Del("Cookie")
		for _, c := range cookies {
			if c.Name == cfg.Cookie {
				c.Name = "__session"
				r.AddCookie(c)
			}
		}
		if strings.HasPrefix(r.URL.Path, "/__dev/") || r.URL.Path == "/sign-in" {
			recorder := &cookieWriter{ResponseWriter: w, name: cfg.Cookie}
			p.browser(recorder, r)
			return
		}
		if r.URL.Path == "/auth/config" && r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, map[string]string{"scriptURL": "/__dev/clerk.js", "publishableKey": p.config(nil).PublishableKey})
			return
		}
		r.Header.Del("X-Forwarded-Proto")
		r.Header.Del("X-Forwarded-For")
		proxy.ServeHTTP(w, r)
	})}
	defer func() { _ = public.Close() }()
	go func() {
		if err := public.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("proxy: %v", err)
			cancel()
		}
	}()
	ready, _ := json.Marshal(map[string]string{"url": origin, "login": origin + "/__dev/?key=" + p.capability, "backend": "http://" + backend, "provider": "http://" + providerHost})
	if err = os.WriteFile(filepath.Join(*state, "ready.json.tmp"), ready, 0o600); err != nil {
		return err
	}
	if err = os.Rename(filepath.Join(*state, "ready.json.tmp"), filepath.Join(*state, "ready.json")); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-srv.Errors():
		return err
	}
}

type cookieWriter struct {
	http.ResponseWriter
	name string
}

func (w *cookieWriter) Write(b []byte) (int, error) { w.rename(); return w.ResponseWriter.Write(b) }
func (w *cookieWriter) WriteHeader(status int)      { w.rename(); w.ResponseWriter.WriteHeader(status) }

func (w *cookieWriter) rename() {
	values := w.Header().Values("Set-Cookie")
	w.Header().Del("Set-Cookie")
	for _, v := range values {
		w.Header().Add("Set-Cookie", strings.Replace(v, "__session=", w.name+"=", 1))
	}
}

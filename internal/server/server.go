package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/handlers"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/importer"
	"ride-home-router/internal/logutil"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/routesession"
	"ride-home-router/internal/routing"
	"ride-home-router/internal/templates"
	"ride-home-router/migrations"
	"ride-home-router/web"
	"strings"
	"time"
)

// Server owns the HTTP server and its dependencies.
type Server struct {
	httpServer   *http.Server
	handler      *handlers.Handler
	db           database.DataStore
	listener     net.Listener
	addr         string
	allowedHosts []string
}

// Config defines server startup settings.
type Config struct {
	Addr string // e.g., "127.0.0.1:8080" or "127.0.0.1:0" for random port
	// AllowedHosts lists proxy hostnames accepted in Host and Origin.
	AllowedHosts []string
	// DatabaseURL points to the Postgres database to migrate and serve.
	DatabaseURL string
	// GoogleMapsAPIKey enables Google Routes distances; empty disables routing.
	GoogleMapsAPIKey string
}

const (
	serverReadTimeout  = 15 * time.Second
	serverWriteTimeout = 60 * time.Second
	serverIdleTimeout  = 120 * time.Second

	maxRequestBodyBytes int64 = 1 << 20

	serverMessageInvalidRequestBody  = "Invalid request body"
	serverMessageForbidden           = "Forbidden"
	serverMessageMethodNotAllowed    = "Method not allowed"
	serverMessageNotFound            = "Not found"
	serverMessageRequestBodyTooLarge = "Request body too large"
)

// New migrates the database and prepares a stopped server.
func New(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.DatabaseURL == "" {
		return nil, errors.New("database URL is required")
	}
	log.Printf("Applying database migrations...")
	if err := migrations.Run(ctx, cfg.DatabaseURL); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}
	db, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize data store: %w", err)
	}

	renderer, err := templates.New(web.Templates)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to load templates: %w", err)
	}

	geocoder := geocoding.NewNominatimGeocoder()
	distanceCalc := distance.NewGoogleCalculator(db.DistanceCache(), func() (string, error) {
		return cfg.GoogleMapsAPIKey, nil
	})
	router := routing.NewBalancedRouter(distanceCalc)
	routeSession := routesession.NewStore(distanceCalc)
	importSession := importer.NewStore(geocoder, db)

	handler := &handlers.Handler{
		DB:            db,
		Geocoder:      geocoder,
		Router:        router,
		Renderer:      renderer,
		RouteSession:  routeSession,
		ImportSession: importSession,
	}

	mux := setupRoutes(handler, web.Static)

	httpServer := &http.Server{
		Addr:         cfg.Addr,
		Handler:      mux,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
		IdleTimeout:  serverIdleTimeout,
	}

	return &Server{
		httpServer:   httpServer,
		handler:      handler,
		db:           db,
		listener:     nil,
		addr:         cfg.Addr,
		allowedHosts: cfg.AllowedHosts,
	}, nil
}

// Start begins serving and returns the listener address.
func (s *Server) Start() (string, error) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", s.addr)
	if err != nil {
		return "", fmt.Errorf("failed to listen: %w", err)
	}

	s.listener = listener
	actualAddr := listener.Addr().String()
	allowlist, err := newRequestAllowlist(actualAddr, s.allowedHosts)
	if err != nil {
		_ = listener.Close()
		return "", err
	}
	s.httpServer.Handler = loggingMiddleware(requestSecurityMiddleware(allowlist, s.httpServer.Handler))
	log.Printf("Starting server on %s", actualAddr)

	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("Server error: %v", err)
		}
	}()

	return actualAddr, nil
}

// Shutdown stops sessions, HTTP serving, and database access.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.handler != nil && s.handler.RouteSession != nil {
		s.handler.RouteSession.Close()
	}
	if s.handler != nil && s.handler.ImportSession != nil {
		s.handler.ImportSession.Close()
	}
	return errors.Join(s.httpServer.Shutdown(ctx), s.db.Close())
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	http.Error(w, serverMessageMethodNotAllowed, http.StatusMethodNotAllowed)
}

func writeNotFound(w http.ResponseWriter) {
	http.Error(w, serverMessageNotFound, http.StatusNotFound)
}

func handleMethods(get, post, put, del http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if get != nil {
				get(w, r)
				return
			}
		case http.MethodPost:
			if post != nil {
				post(w, r)
				return
			}
		case http.MethodPut:
			if put != nil {
				put(w, r)
				return
			}
		case http.MethodDelete:
			if del != nil {
				del(w, r)
				return
			}
		}

		writeMethodNotAllowed(w)
	}
}

func requireMethod(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			writeMethodNotAllowed(w)
			return
		}
		next(w, r)
	}
}

func handleResourcePath(emptyPath, editSuffix string, editHandler, get, put, del http.HandlerFunc) http.HandlerFunc {
	methods := handleMethods(get, nil, put, del)

	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == emptyPath {
			writeNotFound(w)
			return
		}
		if editHandler != nil && editSuffix != "" && strings.HasSuffix(r.URL.Path, editSuffix) && r.Method == http.MethodGet {
			editHandler(w, r)
			return
		}
		methods(w, r)
	}
}

func setupRoutes(handler *handlers.Handler, staticFS fs.FS) *http.ServeMux {
	mux := http.NewServeMux()

	staticSubFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("failed to create static sub-filesystem: %v", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSubFS))))

	mux.HandleFunc("/api/v1/health", handler.HandleHealthCheck)

	mux.HandleFunc("/api/v1/settings", handleMethods(handler.HandleGetSettings, nil, handler.HandleUpdateSettings, nil))
	mux.HandleFunc("/api/v1/imports", handler.HandleCreateImport)
	mux.HandleFunc("/api/v1/imports/", handler.HandleImportSession)
	mux.HandleFunc("/api/v1/participants", handleMethods(handler.HandleListParticipants, handler.HandleCreateParticipant, nil, nil))
	mux.HandleFunc("/api/v1/participants/restore", requireMethod(http.MethodPost, handler.HandleRestoreParticipant))
	mux.HandleFunc("/api/v1/participants/deleted", requireMethod(http.MethodGet, handler.HandleListDeletedParticipants))
	mux.HandleFunc("/api/v1/participants/labels/add", requireMethod(http.MethodPost, handler.HandleAddParticipantsToLabel))
	mux.HandleFunc("/api/v1/participants/labels/remove", requireMethod(http.MethodPost, handler.HandleRemoveParticipantsFromLabel))
	mux.HandleFunc("/api/v1/participants/new", requireMethod(http.MethodGet, handler.HandleParticipantForm))
	mux.HandleFunc("/api/v1/participants/", handleResourcePath("/api/v1/participants/", "/edit", handler.HandleParticipantForm, handler.HandleGetParticipant, handler.HandleUpdateParticipant, handler.HandleDeleteParticipant))
	mux.HandleFunc("/api/v1/drivers", handleMethods(handler.HandleListDrivers, handler.HandleCreateDriver, nil, nil))
	mux.HandleFunc("/api/v1/drivers/restore", requireMethod(http.MethodPost, handler.HandleRestoreDriver))
	mux.HandleFunc("/api/v1/drivers/deleted", requireMethod(http.MethodGet, handler.HandleListDeletedDrivers))
	mux.HandleFunc("/api/v1/drivers/labels/add", requireMethod(http.MethodPost, handler.HandleAddDriversToLabel))
	mux.HandleFunc("/api/v1/drivers/labels/remove", requireMethod(http.MethodPost, handler.HandleRemoveDriversFromLabel))
	mux.HandleFunc("/api/v1/drivers/new", requireMethod(http.MethodGet, handler.HandleDriverForm))
	mux.HandleFunc("/api/v1/drivers/", handleResourcePath("/api/v1/drivers/", "/edit", handler.HandleDriverForm, handler.HandleGetDriver, handler.HandleUpdateDriver, handler.HandleDeleteDriver))
	mux.HandleFunc("/api/v1/labels", handleMethods(handler.HandleListLabels, handler.HandleCreateLabel, nil, nil))
	mux.HandleFunc("/api/v1/labels/new", requireMethod(http.MethodGet, handler.HandleLabelForm))
	mux.HandleFunc("/api/v1/labels/", handleResourcePath("/api/v1/labels/", "/edit", handler.HandleLabelForm, handler.HandleGetLabel, handler.HandleUpdateLabel, handler.HandleDeleteLabel))
	mux.HandleFunc("/api/v1/routes/calculate", requireMethod(http.MethodPost, handler.HandleCalculateRoutes))
	mux.HandleFunc("/api/v1/routes/calculate-with-org-vehicles", requireMethod(http.MethodPost, handler.HandleCalculateRoutesWithOrgVehicles))
	mux.HandleFunc("/api/v1/routes/edit/move-participant", requireMethod(http.MethodPost, handler.HandleMoveParticipant))
	mux.HandleFunc("/api/v1/routes/edit/swap-drivers", requireMethod(http.MethodPost, handler.HandleSwapDrivers))
	mux.HandleFunc("/api/v1/routes/edit/reset", requireMethod(http.MethodPost, handler.HandleResetRoutes))
	mux.HandleFunc("/api/v1/routes/edit/add-driver", requireMethod(http.MethodPost, handler.HandleAddDriver))
	mux.HandleFunc("/api/v1/routes/session", requireMethod(http.MethodGet, handler.HandleGetRouteSession))
	mux.HandleFunc("/api/v1/address-search", requireMethod(http.MethodGet, handler.HandleAddressSearch))
	mux.HandleFunc("/api/v1/activity-locations", handleMethods(handler.HandleListActivityLocations, handler.HandleCreateActivityLocation, nil, nil))
	mux.HandleFunc("/api/v1/activity-locations/restore", requireMethod(http.MethodPost, handler.HandleRestoreActivityLocation))
	mux.HandleFunc("/api/v1/activity-locations/deleted", requireMethod(http.MethodGet, handler.HandleListDeletedActivityLocations))
	mux.HandleFunc("/api/v1/activity-locations/", handleResourcePath("/api/v1/activity-locations/", "/edit", handler.HandleActivityLocationForm, handler.HandleGetActivityLocation, handler.HandleUpdateActivityLocation, handler.HandleDeleteActivityLocation))
	mux.HandleFunc("/api/v1/org-vehicles", handleMethods(handler.HandleListOrgVehicles, handler.HandleCreateOrgVehicle, nil, nil))
	mux.HandleFunc("/api/v1/org-vehicles/", handleResourcePath("/api/v1/org-vehicles/", "/edit", handler.HandleOrgVehicleForm, handler.HandleGetOrgVehicle, handler.HandleUpdateOrgVehicle, handler.HandleDeleteOrgVehicle))
	mux.HandleFunc("/api/v1/events", handleMethods(handler.HandleListEvents, handler.HandleCreateEvent, nil, nil))
	mux.HandleFunc("/api/v1/events/", handleResourcePath("/api/v1/events/", "", nil, handler.HandleGetEvent, nil, handler.HandleDeleteEvent))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handler.HandleIndexPage(w, r)
	})

	mux.HandleFunc("/participants", requireMethod(http.MethodGet, handler.HandleParticipantsPage))
	mux.HandleFunc("/drivers", requireMethod(http.MethodGet, handler.HandleDriversPage))
	mux.HandleFunc("/labels", requireMethod(http.MethodGet, handler.HandleLabelsPage))
	mux.HandleFunc("/activity-locations", requireMethod(http.MethodGet, handler.HandleActivityLocationsPage))
	mux.HandleFunc("/vans", requireMethod(http.MethodGet, handler.HandleVansPage))
	mux.HandleFunc("/settings", requireMethod(http.MethodGet, handler.HandleSettingsPage))
	mux.HandleFunc("/history", requireMethod(http.MethodGet, handler.HandleHistoryPage))

	return mux
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(lrw, r)

		duration := time.Since(start)
		//nolint:gosec // G706: method/path sanitized; local access log only.
		log.Printf(
			"%s %s %d %v",
			logutil.SafeString(r.Method),
			logutil.SafeString(r.URL.Path),
			lrw.statusCode,
			duration,
		)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

type requestAllowlist struct {
	hosts map[string]struct{}
}

// newRequestAllowlist always permits loopback names.
// A public listener requires an explicit proxy hostname.
func newRequestAllowlist(actualAddr string, allowedHosts []string) (requestAllowlist, error) {
	bindHost, port, err := net.SplitHostPort(actualAddr)
	if err != nil {
		return requestAllowlist{}, fmt.Errorf("failed to determine listener address from %q: %w", actualAddr, err)
	}

	bindHost = strings.ToLower(strings.Trim(bindHost, "[]"))
	bindIP := net.ParseIP(bindHost)
	loopbackBind := bindHost == "localhost" || (bindIP != nil && bindIP.IsLoopback())
	if !loopbackBind && len(allowedHosts) == 0 {
		return requestAllowlist{}, fmt.Errorf(
			"listening on non-loopback address %q requires --allowed-hosts naming the public hostname(s) this server is reached by",
			actualAddr,
		)
	}

	names := httpx.LoopbackHostnames()
	if loopbackBind {
		names = append(names, bindHost)
	}

	hosts := make(map[string]struct{})
	for _, name := range names {
		hosts[strings.ToLower(net.JoinHostPort(name, port))] = struct{}{}
		// A bare HTTP host implies port 80.
		if port == "80" {
			hosts[strings.ToLower(bareHost(name))] = struct{}{}
		}
	}
	// Proxy hosts are valid bare and on the listener port.
	for _, name := range allowedHosts {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		hosts[bareHost(name)] = struct{}{}
		hosts[strings.ToLower(net.JoinHostPort(strings.Trim(name, "[]"), port))] = struct{}{}
	}

	return requestAllowlist{hosts: hosts}, nil
}

func bareHost(name string) string {
	if strings.Contains(name, ":") && !strings.HasPrefix(name, "[") {
		return "[" + name + "]"
	}
	return name
}

func (a requestAllowlist) allowsHost(host string) bool {
	_, ok := a.hosts[strings.ToLower(host)]
	return ok
}

func requestSecurityMiddleware(allowlist requestAllowlist, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowlist.allowsHost(r.Host) {
			http.Error(w, serverMessageForbidden, http.StatusForbidden)
			return
		}

		if isStateChangingMethod(r.Method) {
			if !httpx.HasSameOrigin(r) {
				http.Error(w, serverMessageForbidden, http.StatusForbidden)
				return
			}
			contentType := r.Header.Get(httpx.HeaderContentType)
			if !httpx.IsHTMX(r) &&
				!httpx.HasMediaType(contentType, httpx.MediaTypeJSON) &&
				!httpx.HasFormContentType(contentType) {
				http.Error(w, serverMessageForbidden, http.StatusForbidden)
				return
			}

			bodyLimit := maxRequestBodyBytes
			if r.Method == http.MethodPost && r.URL.Path == "/api/v1/imports" {
				bodyLimit = handlers.MaxImportUploadBytes
			}
			limitedBody := http.MaxBytesReader(w, r.Body, bodyLimit)
			body, err := io.ReadAll(limitedBody)
			_ = limitedBody.Close()
			if err != nil {
				if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
					http.Error(w, serverMessageRequestBodyTooLarge, http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, serverMessageInvalidRequestBody, http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
		}

		next.ServeHTTP(w, r)
	})
}

func isStateChangingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

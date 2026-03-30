package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/handlers"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/routing"
	"ride-home-router/internal/sqlite"
	"ride-home-router/internal/templateutil"
	"ride-home-router/internal/testsupport"
	"ride-home-router/web"
)

// Server wraps the HTTP server and all dependencies
type Server struct {
	httpServer *http.Server
	handler    *handlers.Handler
	db         database.DataStore
	listener   net.Listener
	addr       string
	dbPath     string
}

// Config holds server configuration
type Config struct {
	Addr   string // e.g., "127.0.0.1:8080" or "127.0.0.1:0" for random port
	DBPath string // Optional: path to SQLite database, uses config file or default if empty
}

const (
	serverReadTimeout  = 15 * time.Second
	serverWriteTimeout = 60 * time.Second
	serverIdleTimeout  = 120 * time.Second

	serverMessageInvalidRequestBody       = "Invalid request body"
	serverMessageMethodNotAllowed         = "Method not allowed"
	serverMessageNotFound                 = "Not found"
	serverMessageOnlyHTTPHTTPSURLsAllowed = "Only HTTP/HTTPS URLs are allowed"
	serverMessageURLRequired              = "URL is required"
	serverMessageUnsupportedPlatform      = "Unsupported platform"
	serverMessageFailedToOpenURL          = "Failed to open URL"
)

// New creates and initializes a new server (does not start it)
func New(cfg Config) (*Server, error) {
	// Determine database path
	dbPath := cfg.DBPath
	if dbPath == "" {
		// Load from config file or use default
		appConfig, err := database.LoadConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
		dbPath = appConfig.DatabasePath
	}

	log.Printf("Initializing SQLite data store at: %s", dbPath)
	db, err := sqlite.New(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize data store: %w", err)
	}

	log.Printf("Loading templates...")
	templates, err := loadTemplates(web.Templates)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to load templates: %w", err)
	}

	geocoder := geocoding.NewNominatimGeocoder()
	distanceCalc := distance.NewOSRMCalculator(db.DistanceCache())
	if os.Getenv("RHR_E2E_STUB_APIS") == "1" {
		log.Printf("Running with deterministic E2E geocoding and distance stubs")
		geocoder = testsupport.NewE2EGeocoder()
		distanceCalc = testsupport.NewE2EDistanceCalculator()
	}
	router := routing.NewBalancedRouter(distanceCalc)
	routeSession := handlers.NewRouteSessionStore()

	handler := &handlers.Handler{
		DB:           db,
		Geocoder:     geocoder,
		DistanceCalc: distanceCalc,
		Router:       router,
		Templates:    templates,
		RouteSession: routeSession,
	}

	mux := setupRoutes(handler, web.Static)

	httpServer := &http.Server{
		Addr:         cfg.Addr,
		Handler:      loggingMiddleware(corsMiddleware(mux)),
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
		IdleTimeout:  serverIdleTimeout,
	}

	return &Server{
		httpServer: httpServer,
		handler:    handler,
		db:         db,
		listener:   nil,
		addr:       cfg.Addr,
		dbPath:     dbPath,
	}, nil
}

// GetDBPath returns the current database path
func (s *Server) GetDBPath() string {
	return s.dbPath
}

// Start starts the server and returns the actual address (useful for random port)
func (s *Server) Start() (string, error) {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return "", fmt.Errorf("failed to listen: %w", err)
	}

	s.listener = listener
	actualAddr := listener.Addr().String()
	log.Printf("Starting server on %s", actualAddr)

	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("Server error: %v", err)
		}
	}()

	return actualAddr, nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	if s.handler != nil && s.handler.RouteSession != nil {
		s.handler.RouteSession.Close()
	}
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}
	return s.db.Close()
}

// loadTemplates loads all templates from the embedded filesystem
func loadTemplates(templatesFS fs.FS) (*handlers.TemplateSet, error) {
	funcs := templateutil.FuncMap()
	base := template.New("").Funcs(funcs)

	// Load layout.html
	layoutContent, err := fs.ReadFile(templatesFS, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("failed to read layout: %w", err)
	}
	_, err = base.New("layout.html").Parse(string(layoutContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse layout: %w", err)
	}

	// Load partials
	partialFiles, err := fs.Glob(templatesFS, "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to glob partials: %w", err)
	}

	for _, file := range partialFiles {
		content, err := fs.ReadFile(templatesFS, file)
		if err != nil {
			return nil, fmt.Errorf("failed to read partial %s: %w", file, err)
		}
		// Extract just the filename from the path
		name := file[len("templates/partials/"):]
		_, err = base.New(name).Parse(string(content))
		if err != nil {
			return nil, fmt.Errorf("failed to parse partial %s: %w", file, err)
		}
	}

	// Load page templates as strings (don't parse into base)
	pages := make(map[string]string)
	pageFiles := []string{"index.html", "participants.html", "drivers.html", "activity_locations.html", "vans.html", "settings.html", "history.html"}
	for _, name := range pageFiles {
		content, err := fs.ReadFile(templatesFS, "templates/"+name)
		if err != nil {
			return nil, fmt.Errorf("failed to read page %s: %w", name, err)
		}
		pages[name] = string(content)
	}

	return &handlers.TemplateSet{
		Base:  base,
		Pages: pages,
		Funcs: funcs,
	}, nil
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

// setupRoutes configures all HTTP routes
func setupRoutes(handler *handlers.Handler, staticFS fs.FS) *http.ServeMux {
	mux := http.NewServeMux()

	// Serve static files from embedded filesystem
	staticSubFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("failed to create static sub-filesystem: %v", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSubFS))))

	mux.HandleFunc("/api/v1/health", handler.HandleHealthCheck)

	mux.HandleFunc("/api/v1/open-url", requireMethod(http.MethodPost, handleOpenURL))
	mux.HandleFunc("/api/v1/settings", handleMethods(handler.HandleGetSettings, nil, handler.HandleUpdateSettings, nil))
	mux.HandleFunc("/api/v1/config/database", handleMethods(handler.HandleGetDatabaseConfig, nil, handler.HandleUpdateDatabaseConfig, nil))
	mux.HandleFunc("/api/v1/participants", handleMethods(handler.HandleListParticipants, handler.HandleCreateParticipant, nil, nil))
	mux.HandleFunc("/api/v1/participants/new", requireMethod(http.MethodGet, handler.HandleParticipantForm))
	mux.HandleFunc("/api/v1/participants/", handleResourcePath("/api/v1/participants/", "/edit", handler.HandleParticipantForm, handler.HandleGetParticipant, handler.HandleUpdateParticipant, handler.HandleDeleteParticipant))
	mux.HandleFunc("/api/v1/drivers", handleMethods(handler.HandleListDrivers, handler.HandleCreateDriver, nil, nil))
	mux.HandleFunc("/api/v1/drivers/new", requireMethod(http.MethodGet, handler.HandleDriverForm))
	mux.HandleFunc("/api/v1/drivers/", handleResourcePath("/api/v1/drivers/", "/edit", handler.HandleDriverForm, handler.HandleGetDriver, handler.HandleUpdateDriver, handler.HandleDeleteDriver))
	mux.HandleFunc("/api/v1/routes/calculate", requireMethod(http.MethodPost, handler.HandleCalculateRoutes))
	mux.HandleFunc("/api/v1/routes/calculate-with-org-vehicles", requireMethod(http.MethodPost, handler.HandleCalculateRoutesWithOrgVehicles))
	mux.HandleFunc("/api/v1/routes/edit/move-participant", requireMethod(http.MethodPost, handler.HandleMoveParticipant))
	mux.HandleFunc("/api/v1/routes/edit/swap-drivers", requireMethod(http.MethodPost, handler.HandleSwapDrivers))
	mux.HandleFunc("/api/v1/routes/edit/reset", requireMethod(http.MethodPost, handler.HandleResetRoutes))
	mux.HandleFunc("/api/v1/routes/edit/add-driver", requireMethod(http.MethodPost, handler.HandleAddDriver))
	mux.HandleFunc("/api/v1/routes/session", requireMethod(http.MethodGet, handler.HandleGetRouteSession))
	mux.HandleFunc("/api/v1/address-search", requireMethod(http.MethodGet, handler.HandleAddressSearch))
	mux.HandleFunc("/api/v1/activity-locations", handleMethods(handler.HandleListActivityLocations, handler.HandleCreateActivityLocation, nil, nil))
	mux.HandleFunc("/api/v1/activity-locations/", handleResourcePath("/api/v1/activity-locations/", "/edit", handler.HandleActivityLocationForm, handler.HandleGetActivityLocation, handler.HandleUpdateActivityLocation, handler.HandleDeleteActivityLocation))
	mux.HandleFunc("/api/v1/org-vehicles", handleMethods(handler.HandleListOrgVehicles, handler.HandleCreateOrgVehicle, nil, nil))
	mux.HandleFunc("/api/v1/org-vehicles/", handleResourcePath("/api/v1/org-vehicles/", "/edit", handler.HandleOrgVehicleForm, handler.HandleGetOrgVehicle, handler.HandleUpdateOrgVehicle, handler.HandleDeleteOrgVehicle))
	mux.HandleFunc("/api/v1/events", handleMethods(handler.HandleListEvents, handler.HandleCreateEvent, nil, nil))
	mux.HandleFunc("/api/v1/events/", handleResourcePath("/api/v1/events/", "", nil, handler.HandleGetEvent, nil, handler.HandleDeleteEvent))

	// Page routes
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handler.HandleIndexPage(w, r)
	})

	mux.HandleFunc("/participants", requireMethod(http.MethodGet, handler.HandleParticipantsPage))
	mux.HandleFunc("/drivers", requireMethod(http.MethodGet, handler.HandleDriversPage))
	mux.HandleFunc("/activity-locations", requireMethod(http.MethodGet, handler.HandleActivityLocationsPage))
	mux.HandleFunc("/vans", requireMethod(http.MethodGet, handler.HandleVansPage))
	mux.HandleFunc("/settings", requireMethod(http.MethodGet, handler.HandleSettingsPage))
	mux.HandleFunc("/history", requireMethod(http.MethodGet, handler.HandleHistoryPage))

	return mux
}

// handleOpenURL opens a URL in the system's default browser
func handleOpenURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, serverMessageInvalidRequestBody, http.StatusBadRequest)
		return
	}

	if req.URL == "" {
		http.Error(w, serverMessageURLRequired, http.StatusBadRequest)
		return
	}

	// Only allow http/https URLs for security
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		http.Error(w, serverMessageOnlyHTTPHTTPSURLsAllowed, http.StatusBadRequest)
		return
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", req.URL)
	case "darwin":
		cmd = exec.Command("open", req.URL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", req.URL)
	default:
		http.Error(w, serverMessageUnsupportedPlatform, http.StatusInternalServerError)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("Failed to open URL: %v", err)
		http.Error(w, serverMessageFailedToOpenURL, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(lrw, r)

		duration := time.Since(start)
		log.Printf("%s %s %d %v", r.Method, r.URL.Path, lrw.statusCode, duration)
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

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Only allow localhost origins (Wails webview and local development)
		if origin == "" ||
			strings.HasPrefix(origin, "http://localhost:") ||
			strings.HasPrefix(origin, "http://127.0.0.1:") ||
			strings.HasPrefix(origin, "wails://") {
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", strings.Join([]string{
				httpx.HeaderContentType,
				httpx.HeaderHXRequest,
				httpx.HeaderHXTarget,
				httpx.HeaderHXCurrentURL,
			}, ", "))
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

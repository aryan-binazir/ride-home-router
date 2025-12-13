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
	"os/exec"
	"runtime"
	"strings"
	"time"

	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/handlers"
	"ride-home-router/internal/routing"
	"ride-home-router/web"
)

// Distance conversion constants
const (
	MetersPerMile      = 1609.344
	MetersPerKilometer = 1000.0
)

// Server wraps the HTTP server and all dependencies
type Server struct {
	httpServer *http.Server
	handler    *handlers.Handler
	db         *database.JSONStore
	listener   net.Listener
	addr       string
}

// Config holds server configuration
type Config struct {
	Addr string // e.g., "127.0.0.1:8080" or "127.0.0.1:0" for random port
}

// New creates and initializes a new server (does not start it)
func New(cfg Config) (*Server, error) {
	log.Printf("Initializing distance cache...")
	distanceCache, err := database.NewFileDistanceCache()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize distance cache: %w", err)
	}

	log.Printf("Initializing data store...")
	db, err := database.NewJSONStore(distanceCache)
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
	router := routing.NewDistanceMinimizer(distanceCalc)
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
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return &Server{
		httpServer: httpServer,
		handler:    handler,
		db:         db,
		addr:       cfg.Addr,
	}, nil
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
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}
	return s.db.Close()
}

// Template helper functions
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"formatDate": func(t time.Time) string {
			return t.Format("2006-01-02")
		},
		"add": func(a, b int) int {
			return a + b
		},
		"currentDate": func() string {
			return time.Now().Format("2006-01-02")
		},
		"toJSON": func(v interface{}) string {
			b, err := json.Marshal(v)
			if err != nil {
				return "{}"
			}
			return string(b)
		},
		"formatDistance": func(meters float64, useMiles bool) string {
			if useMiles {
				miles := meters / MetersPerMile
				return fmt.Sprintf("%.2f mi", miles)
			}
			km := meters / MetersPerKilometer
			return fmt.Sprintf("%.2f km", km)
		},
		"formatDuration": func(seconds float64) string {
			mins := int(seconds / 60)
			secs := int(seconds) % 60
			if mins == 0 {
				return fmt.Sprintf("%ds", secs)
			}
			if secs == 0 {
				return fmt.Sprintf("%dm", mins)
			}
			return fmt.Sprintf("%dm %ds", mins, secs)
		},
		"initials": func(name string) string {
			parts := strings.Fields(strings.TrimSpace(name))
			if len(parts) == 0 {
				return ""
			}

			first := []rune(parts[0])
			if len(parts) == 1 {
				if len(first) == 0 {
					return ""
				}
				return strings.ToUpper(string(first[0]))
			}

			last := []rune(parts[len(parts)-1])
			if len(first) == 0 || len(last) == 0 {
				return ""
			}
			return strings.ToUpper(string(first[0]) + string(last[0]))
		},
	}
}

// loadTemplates loads all templates from the embedded filesystem
func loadTemplates(templatesFS fs.FS) (*handlers.TemplateSet, error) {
	funcs := templateFuncs()
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
	pageFiles := []string{"index.html", "participants.html", "drivers.html", "settings.html", "history.html"}
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

	mux.HandleFunc("/api/v1/open-url", handleOpenURL)

	mux.HandleFunc("/api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.HandleGetSettings(w, r)
		case http.MethodPut:
			handler.HandleUpdateSettings(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/participants", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.HandleListParticipants(w, r)
		case http.MethodPost:
			handler.HandleCreateParticipant(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/participants/new", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleParticipantForm(w, r)
	})

	mux.HandleFunc("/api/v1/participants/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/participants/" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		// Check for /edit route
		if strings.HasSuffix(r.URL.Path, "/edit") && r.Method == http.MethodGet {
			handler.HandleParticipantForm(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			handler.HandleGetParticipant(w, r)
		case http.MethodPut:
			handler.HandleUpdateParticipant(w, r)
		case http.MethodDelete:
			handler.HandleDeleteParticipant(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/drivers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.HandleListDrivers(w, r)
		case http.MethodPost:
			handler.HandleCreateDriver(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/drivers/new", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleDriverForm(w, r)
	})

	mux.HandleFunc("/api/v1/drivers/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/drivers/" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		// Check for /edit route
		if strings.HasSuffix(r.URL.Path, "/edit") && r.Method == http.MethodGet {
			handler.HandleDriverForm(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			handler.HandleGetDriver(w, r)
		case http.MethodPut:
			handler.HandleUpdateDriver(w, r)
		case http.MethodDelete:
			handler.HandleDeleteDriver(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/routes/calculate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleCalculateRoutes(w, r)
	})

	mux.HandleFunc("/api/v1/routes/calculate-with-org-vehicles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleCalculateRoutesWithOrgVehicles(w, r)
	})

	mux.HandleFunc("/api/v1/routes/edit/move-participant", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleMoveParticipant(w, r)
	})

	mux.HandleFunc("/api/v1/routes/edit/swap-drivers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleSwapDrivers(w, r)
	})

	mux.HandleFunc("/api/v1/routes/edit/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleResetRoutes(w, r)
	})

	mux.HandleFunc("/api/v1/routes/edit/add-driver", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleAddDriver(w, r)
	})

	mux.HandleFunc("/api/v1/address-search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleAddressSearch(w, r)
	})

	mux.HandleFunc("/api/v1/activity-locations", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.HandleListActivityLocations(w, r)
		case http.MethodPost:
			handler.HandleCreateActivityLocation(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/activity-locations/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/activity-locations/" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		switch r.Method {
		case http.MethodDelete:
			handler.HandleDeleteActivityLocation(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Organization Vehicles routes
	mux.HandleFunc("/api/v1/org-vehicles", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.HandleListOrgVehicles(w, r)
		case http.MethodPost:
			handler.HandleCreateOrgVehicle(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/org-vehicles/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/org-vehicles/" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		switch r.Method {
		case http.MethodPut:
			handler.HandleUpdateOrgVehicle(w, r)
		case http.MethodDelete:
			handler.HandleDeleteOrgVehicle(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.HandleListEvents(w, r)
		case http.MethodPost:
			handler.HandleCreateEvent(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/events/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/events/" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		switch r.Method {
		case http.MethodGet:
			handler.HandleGetEvent(w, r)
		case http.MethodDelete:
			handler.HandleDeleteEvent(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Page routes
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handler.HandleIndexPage(w, r)
	})

	mux.HandleFunc("/participants", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleParticipantsPage(w, r)
	})

	mux.HandleFunc("/drivers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleDriversPage(w, r)
	})

	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleSettingsPage(w, r)
	})

	mux.HandleFunc("/history", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleHistoryPage(w, r)
	})

	return mux
}

// handleOpenURL opens a URL in the system's default browser
func handleOpenURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.URL == "" {
		http.Error(w, "URL is required", http.StatusBadRequest)
		return
	}

	// Only allow http/https URLs for security
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		http.Error(w, "Only HTTP/HTTPS URLs are allowed", http.StatusBadRequest)
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
		http.Error(w, "Unsupported platform", http.StatusInternalServerError)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("Failed to open URL: %v", err)
		http.Error(w, "Failed to open URL", http.StatusInternalServerError)
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
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, HX-Request, HX-Target, HX-Current-URL")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

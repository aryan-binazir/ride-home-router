package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/handlers"
	"ride-home-router/internal/routing"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("Fatal error: %v", err)
	}
}

// Distance conversion constants
const (
	MetersPerMile      = 1609.344
	MetersPerKilometer = 1000.0
)

// Template helper functions
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"formatDate": func(t time.Time) string {
			return t.Format("2006-01-02")
		},
		"divideFloat": func(a float64, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
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
		"distanceUnit": func(useMiles bool) string {
			if useMiles {
				return "mi"
			}
			return "km"
		},
	}
}

// loadTemplates loads all templates from the filesystem
func loadTemplates(templatesDir string) (*handlers.TemplateSet, error) {
	funcs := templateFuncs()
	base := template.New("").Funcs(funcs)

	// Load layout.html
	layoutPath := filepath.Join(templatesDir, "layout.html")
	layoutContent, err := os.ReadFile(layoutPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read layout: %w", err)
	}
	_, err = base.New("layout.html").Parse(string(layoutContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse layout: %w", err)
	}

	// Load partials
	partialsPattern := filepath.Join(templatesDir, "partials", "*.html")
	partialFiles, err := filepath.Glob(partialsPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob partials: %w", err)
	}

	for _, file := range partialFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read partial %s: %w", file, err)
		}
		_, err = base.New(filepath.Base(file)).Parse(string(content))
		if err != nil {
			return nil, fmt.Errorf("failed to parse partial %s: %w", file, err)
		}
	}

	// Load page templates as strings (don't parse into base)
	pages := make(map[string]string)
	pageFiles := []string{"index.html", "participants.html", "drivers.html", "settings.html", "history.html"}
	for _, name := range pageFiles {
		path := filepath.Join(templatesDir, name)
		content, err := os.ReadFile(path)
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

func run() error {
	dbPath := getEnv("DATABASE_PATH", "ride-home-router.db")
	addr := getEnv("SERVER_ADDR", "127.0.0.1:8080")

	log.Printf("Initializing database at %s", dbPath)
	db, err := database.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer db.Close()

	log.Printf("Loading templates...")
	templatesDir := getEnv("TEMPLATES_DIR", "web/templates")
	templates, err := loadTemplates(templatesDir)
	if err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	geocoder := geocoding.NewNominatimGeocoder()
	distanceCalc := distance.NewOSRMCalculator(db.DistanceCacheRepository)
	router := routing.NewGreedyRouter(distanceCalc)

	handler := &handlers.Handler{
		DB:           db,
		Geocoder:     geocoder,
		DistanceCalc: distanceCalc,
		Router:       router,
		Templates:    templates,
	}

	mux := http.NewServeMux()

	// Serve static files
	staticDir := getEnv("STATIC_DIR", "web/static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	mux.HandleFunc("/api/v1/health", handler.HandleHealthCheck)

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

	mux.HandleFunc("/api/v1/geocode", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.HandleGeocodeAddress(w, r)
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

	server := &http.Server{
		Addr:         addr,
		Handler:      loggingMiddleware(corsMiddleware(mux)),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("Starting server on %s", addr)
		serverErrors <- server.ListenAndServe()
	}()

	// Open browser after a short delay to ensure server is ready
	go func() {
		time.Sleep(500 * time.Millisecond)
		url := fmt.Sprintf("http://%s", addr)
		if err := openBrowser(url); err != nil {
			log.Printf("Could not open browser: %v", err)
		} else {
			log.Printf("Opened browser at %s", url)
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)

	case sig := <-shutdown:
		log.Printf("Received signal %v, starting graceful shutdown", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			server.Close()
			return fmt.Errorf("could not gracefully shutdown the server: %w", err)
		}

		log.Println("Server stopped")
	}

	return nil
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
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, HX-Request, HX-Target, HX-Current-URL")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
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

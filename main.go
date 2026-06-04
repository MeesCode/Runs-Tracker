package main

import (
	"database/sql"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"
)

//go:embed data/strava-export.json
var seedData []byte

//go:embed all:frontend/dist
var frontendFS embed.FS

// App holds shared dependencies for the HTTP handlers.
type App struct {
	db *sql.DB
}

func main() {
	port := getenv("PORT", "8651")
	dbPath := getenv("DB_PATH", "runs.db")

	db, err := openDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := seedFromExport(db, seedData); err != nil {
		log.Fatalf("seed: %v", err)
	}

	app := &App{db: db}
	mux := http.NewServeMux()

	// API routes.
	mux.HandleFunc("/api/runs", app.handleRuns)        // list (also catches trailing)
	mux.HandleFunc("/api/runs/", app.handleRunByPath)  // /api/runs/:id and /:id/gpx
	mux.HandleFunc("/api/stats", app.handleStats)
	mux.HandleFunc("/api/investigate/strava", app.handleInvestigateStrava)
	mux.HandleFunc("/api/investigate/health", app.handleInvestigateHealth)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Static frontend (embedded Vite build).
	mux.Handle("/", spaHandler())

	handler := withCORS(mux)
	addr := ":" + port
	log.Printf("runs-tracker listening on %s (db=%s)", addr, dbPath)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}

// spaHandler serves the embedded frontend, falling back to index.html so
// client-side routes (e.g. /stats) resolve.
func spaHandler() http.Handler {
	sub, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		log.Fatalf("frontend fs: %v", err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If the requested asset exists, serve it; otherwise serve index.html.
		p := r.URL.Path
		if p != "/" {
			if _, err := fs.Stat(sub, trimLeadingSlash(p)); err != nil {
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/"
				fileServer.ServeHTTP(w, r2)
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

func trimLeadingSlash(p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p[1:]
	}
	return p
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// nowLocal returns the current time in the app's reference timezone.
func nowLocal() time.Time {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		return time.Now()
	}
	return time.Now().In(loc)
}

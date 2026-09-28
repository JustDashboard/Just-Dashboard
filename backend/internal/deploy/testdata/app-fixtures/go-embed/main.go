package main

import (
	"database/sql"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"example.com/goembed/views"
	"github.com/a-h/templ"
	_ "github.com/mattn/go-sqlite3"
)

// The front end is a Vite build the checkout does not commit.
//
//go:embed all:web/dist
var site embed.FS

func main() {
	db, err := sql.Open("sqlite3", "file::memory:")
	if err != nil {
		log.Fatal(err)
	}
	var version string
	// go-sqlite3 built without cgo is a stub whose first query fails.
	if err := db.QueryRow("select sqlite_version()").Scan(&version); err != nil {
		log.Fatal(err)
	}
	zone, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		log.Fatal(err)
	}
	dist, err := fs.Sub(site, "web/dist")
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/", http.FileServerFS(dist))
	http.Handle("/status", templ.Handler(views.Status(version, zone.String())))
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"example.com/shared"
)

// The page is a template the service reads from its working directory at
// runtime, filled with a value from a sibling module of the go.work.
func main() {
	page, err := os.ReadFile("templates/index.html")
	if err != nil {
		log.Fatal(err)
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(strings.ReplaceAll(string(page), "{{value}}", shared.Value())))
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

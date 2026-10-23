// Command ipmweb serves the Mastercard IPM and Visa BASE II dashboard.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"mciipm/internal/webapp"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "address to listen on")
	flag.Parse()

	srv, err := webapp.NewServer()
	if err != nil {
		log.Fatalf("failed to start server: %v", err)
	}

	// Allow large uploads while bounding how long a connection can stay open.
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}

	fmt.Printf("mciipm dashboard running at http://%s\n", *addr)
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

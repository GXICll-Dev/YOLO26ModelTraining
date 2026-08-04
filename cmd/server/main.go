package main

import (
	"log"
	"net/http"
	"os"

	"modeltraining-go-ts/internal/app"
)

func main() {
	addr := getenv("MT_ADDR", "127.0.0.1:8080")
	baseDir := getenv("MT_BASE_DIR", ".")

	server, err := app.NewServer(baseDir)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("ModelTraining Go+TS server listening on http://%s", addr)
	if err := http.ListenAndServe(addr, server.Routes()); err != nil {
		log.Fatal(err)
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

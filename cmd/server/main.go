package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"arcticfreight/internal/app"
	"arcticfreight/internal/httpapi"
	"arcticfreight/internal/scheduler"
	"arcticfreight/internal/store"
)

func main() {
	dataPath := envOr("ARCTICFREIGHT_DATA_PATH", "/tmp/arcticfreight-state.json")
	port := envOr("ARCTICFREIGHT_PORT", "51108")
	checkInterval := envOrDuration("ARCTICFREIGHT_CHECK_INTERVAL", time.Minute)

	st := store.New(dataPath)
	svc := app.New(st)
	sched := scheduler.New(svc, checkInterval)
	sched.Start()
	defer sched.Stop()

	handler := httpapi.NewHandler(svc)
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		log.Printf("arcticfreight server listening on :%s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

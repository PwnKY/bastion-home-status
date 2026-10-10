package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/PwnKY/bastion-home-status/internal/status"
)

func main() {
	path := flag.String("config", "", "private server configuration file")
	flag.Parse()
	if *path == "" {
		log.Fatal("-config is required")
	}
	file, err := os.Open(*path)
	if err != nil {
		log.Fatal("cannot open configuration")
	}
	var config status.Config
	decoder := json.NewDecoder(io.LimitReader(file, 128*1024))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&config)
	file.Close()
	if err != nil {
		log.Fatal("invalid configuration")
	}
	if err = config.Validate(); err != nil {
		log.Fatal(err)
	}
	if err = status.ValidateListen(config.Listen); err != nil {
		log.Fatal(err)
	}
	store, err := status.Open(config)
	if err != nil {
		log.Fatal("cannot initialize storage")
	}
	defer store.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: config.Listen, Handler: status.NewHandler(store), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		lastCleanup := time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if store.Reconcile(now) != nil {
					log.Print("state reconciliation failed")
				}
				if now.Sub(lastCleanup) >= time.Hour {
					if store.Cleanup(now) != nil {
						log.Print("retention cleanup failed")
					} else {
						lastCleanup = now
					}
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		deadline, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(deadline)
	}()
	log.Print("bastion backend starting; private listener; no request bodies or credentials logged")
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("HTTP server failed")
	}
}

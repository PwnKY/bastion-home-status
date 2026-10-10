package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/PwnKY/bastion-home-status/internal/collector"
)

func main() {
	path := flag.String("config", "", "private collector configuration file")
	flag.Parse()
	if *path == "" {
		log.Fatal("-config is required")
	}
	file, err := os.Open(*path)
	if err != nil {
		log.Fatal("cannot open collector configuration")
	}
	var config collector.Config
	decoder := json.NewDecoder(io.LimitReader(file, 128*1024))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&config)
	file.Close()
	if err != nil {
		log.Fatal("invalid collector configuration")
	}
	agent, err := collector.New(config)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Print("collector starting; fixed private configuration; no probe bodies or credentials logged")
	agent.Run(ctx)
}

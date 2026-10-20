package main

import (
	"flag"
	"log"
	"paypal/server/internal/config"
	"paypal/server/internal/httpserver"
)

func main() {
	path := flag.String("config", "configs/local.json", "server configuration file")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("starting %s server on %s", cfg.Mode, cfg.HTTP.Address)
	httpserver.New(cfg).Spin()
}

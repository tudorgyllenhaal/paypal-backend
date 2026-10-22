package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"paypal/server/internal/config"
	"paypal/server/internal/database"
	"paypal/server/internal/httpserver"
)

func main() {
	path := flag.String("config", "configs/local.json", "server configuration file")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		log.Fatal(err)
	}
	db := initMySQL(cfg.MySQL)
	defer db.Close()
	log.Printf("starting %s server on %s", cfg.Mode, cfg.HTTP.Address)
	httpserver.New(cfg).Spin()
}

func initMySQL(cfg config.MySQL) *sql.DB {
	db, err := database.OpenMySQL(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	log.Print("MySQL connection initialized")
	return db
}

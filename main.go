package main

import (
	"github.com/jmcgill/kitchensync/kitchensync"
	"flag"
	"log"
)

type Config struct {
	reset  bool
	clean bool
	db string
}

var config *Config

func init() {
	const (
		resetDefault = false
		resetDescription  = "Reset all specified fields"

		cleanDefault = false
		cleanDescription = "Drop all data and reset the database to the specified state"

		db = ""
		dbDescription = "The connection string for the database"
	)

	config = &Config{}
	flag.BoolVar(&config.reset, "reset", resetDefault, resetDescription)
	flag.BoolVar(&config.clean, "clean", cleanDefault, cleanDescription)
	flag.StringVar(&config.db, "db", db, dbDescription)
}

func main() {
	flag.Parse()

	log.Printf("Connecting to db: %v", config.db)

	dbURI := config.db
	k, err := kitchensync.NewKitchenSync(".", dbURI, true)
	if err != nil {
		panic(err)
	}

	if config.clean {
		k.Drop()
	}

	err = k.Sync(config.reset)
	if err != nil {
		panic(err)
	}
}

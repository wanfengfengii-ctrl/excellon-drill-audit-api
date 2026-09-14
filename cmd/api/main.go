// Command api runs the drill-file statistics HTTP service.
package main

import (
	"log"
	"os"

	"drillapi/internal/api"
)

func main() {
	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}

	addr := ":" + port
	log.Printf("drill statistics API listening on %s", addr)
	if err := api.Router().Run(addr); err != nil {
		log.Fatal(err)
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/kbipinkumar/TP-Link-ONU-monitor/internal/scraper"
	"github.com/kbipinkumar/TP-Link-ONU-monitor/internal/web"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: onu-monitor <web|scrape>")
		os.Exit(1)
	}

	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("Failed to get executable path: %v", err)
	}
	configPath := filepath.Join(filepath.Dir(exePath), "onu_config.ini")
	
	command := os.Args[1]

	switch command {
	case "web":
		web.Init(configPath)
		web.StartServer("8991")
	case "scrape":
		log.Println("Starting ONU GPON Status Monitor...")
		config, err := scraper.LoadConfig(configPath)
		if err != nil {
			log.Fatalf("[FATAL] Configuration file not found or unreadable: %v", err)
		}
		
		stats, err := scraper.GetGPONStats(config)
		if err != nil {
			log.Printf("Failed to retrieve statistics: %v", err)
			os.Exit(1)
		}
		
		statsJson, _ := json.MarshalIndent(stats, "", "  ")
		log.Printf("Parsed Stats: %s", string(statsJson))
		
		if config.MQTT.Enable {
			scraper.PublishMQTT(stats, config)
		}
		if config.INFLUXDB.Enable {
			scraper.PublishInfluxDB(stats, config)
		}
	default:
		fmt.Printf("Unknown command: %s\n", command)
		fmt.Println("Usage: onu-monitor <web|scrape>")
		os.Exit(1)
	}
}

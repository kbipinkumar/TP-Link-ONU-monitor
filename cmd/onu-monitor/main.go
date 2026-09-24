package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

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
		statusPath := filepath.Join(filepath.Dir(exePath), "status.json")
		status, _ := scraper.ReadStatus(statusPath)
		if status == nil {
			status = &scraper.SystemStatus{}
		}
		
		config, err := scraper.LoadConfig(configPath)
		if err != nil {
			status.LastError = fmt.Sprintf("Config error: %v", err)
			scraper.WriteStatus(statusPath, status)
			log.Fatalf("[FATAL] Configuration file not found or unreadable: %v", err)
		}
		
		if config.ONU.Password == "YOUR_ROUTER_PASSWORD" || config.ONU.Password == "" {
			log.Println("[INFO] Setup incomplete. Please complete the Web UI setup to configure the router password.")
			status.LastError = "Setup incomplete. Please complete the Web UI setup."
			scraper.WriteStatus(statusPath, status)
			os.Exit(0) // Exit cleanly so systemd doesn't mark it as a failure loop
		}
		
		stats, err := scraper.GetGPONStats(config)
		if err != nil {
			status.LastError = fmt.Sprintf("Scrape error: %v", err)
			scraper.WriteStatus(statusPath, status)
			log.Printf("Failed to retrieve statistics: %v", err)
			os.Exit(1)
		}
		
		statsJson, _ := json.MarshalIndent(stats, "", "  ")
		log.Printf("Parsed Stats: %s", string(statsJson))
		
		now := time.Now().Format(time.RFC3339)
		status.LastScrapeTime = now
		status.LastError = "" // clear previous errors
		status.Stats = stats
		
		if config.MQTT.Enable {
			scraper.PublishMQTT(stats, config)
			status.LastMqttTime = now
		}
		if config.INFLUXDB.Enable {
			scraper.PublishInfluxDB(stats, config)
			status.LastInfluxTime = now
		}
		
		scraper.WriteStatus(statusPath, status)
	default:
		fmt.Printf("Unknown command: %s\n", command)
		fmt.Println("Usage: onu-monitor <web|scrape>")
		os.Exit(1)
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/kbipinkumar/TP-Link-ONU-monitor/internal/cli"
	"github.com/kbipinkumar/TP-Link-ONU-monitor/internal/scraper"
	"github.com/kbipinkumar/TP-Link-ONU-monitor/internal/web"
	"gopkg.in/ini.v1"
)

var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: onu-monitor <setup|web|scrape|reset-password|import|version>")
		os.Exit(1)
	}

	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("Failed to get executable path: %v", err)
	}
	configPath := filepath.Join(filepath.Dir(exePath), "onu_config.ini")
	
	command := os.Args[1]

	switch command {
	case "import":
		if len(os.Args) < 3 {
			fmt.Println("Usage: onu-monitor import <path/to/backup.ini>")
			os.Exit(1)
		}
		backupPath := os.Args[2]
		cfg, err := ini.Load(backupPath)
		if err != nil {
			log.Fatalf("Failed to load backup config file '%s': %v", backupPath, err)
		}
		if err := cfg.SaveTo(configPath); err != nil {
			log.Fatalf("Failed to save imported config to '%s': %v", configPath, err)
		}
		fmt.Printf("Configuration successfully imported from '%s' to '%s'\n", backupPath, configPath)
	case "setup":
		cli.RunInteractiveSetup(configPath)
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
			if wErr := scraper.WriteStatus(statusPath, status); wErr != nil {
				log.Printf("Failed to write status to %s: %v", statusPath, wErr)
			}
			log.Fatalf("[FATAL] Configuration file not found or unreadable: %v", err)
		}
		
		if config.ONU.Password == "YOUR_ROUTER_PASSWORD" || config.ONU.Password == "" {
			log.Println("[INFO] Setup incomplete. Please complete the Web UI setup to configure the router password.")
			status.LastError = "Setup incomplete. Please complete the Web UI setup."
			if wErr := scraper.WriteStatus(statusPath, status); wErr != nil {
				log.Printf("Failed to write status to %s: %v", statusPath, wErr)
			}
			os.Exit(0) // Exit cleanly so systemd doesn't mark it as a failure loop
		}
		
		stats, err := scraper.GetGPONStats(config)
		if err != nil {
			status.LastError = fmt.Sprintf("Scrape error: %v", err)
			if wErr := scraper.WriteStatus(statusPath, status); wErr != nil {
				log.Printf("Failed to write status to %s: %v", statusPath, wErr)
			}
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
			if err := scraper.PublishMQTT(stats, config); err != nil {
				status.LastError = fmt.Sprintf("MQTT error: %v", err)
			} else {
				status.LastMqttTime = now
			}
		}
		if config.INFLUXDB.Enable {
			if err := scraper.PublishInfluxDB(stats, config); err != nil {
				if status.LastError != "" {
					status.LastError += "; "
				}
				status.LastError += fmt.Sprintf("InfluxDB error: %v", err)
			} else {
				status.LastInfluxTime = now
			}
		}
		
		if wErr := scraper.WriteStatus(statusPath, status); wErr != nil {
			log.Printf("Failed to write status to %s: %v", statusPath, wErr)
		}
	case "reset-password":
		cfg, err := ini.Load(configPath)
		if err != nil {
			log.Fatalf("Failed to load config file: %v", err)
		}
		cfg.Section("WEBUI").Key("USERNAME").SetValue("")
		cfg.Section("WEBUI").Key("PASSWORD").SetValue("")
		if err := cfg.SaveTo(configPath); err != nil {
			log.Fatalf("Failed to save config file: %v", err)
		}
		fmt.Println("Web UI credentials have been successfully reset.")
		fmt.Println("Please navigate to the Web UI to set up a new username and password.")
	case "version":
		fmt.Printf("onu-monitor version %s\n", Version)
	default:
		fmt.Printf("Unknown command: %s\n", command)
		fmt.Println("Usage: onu-monitor <setup|web|scrape|reset-password|import|version>")
		os.Exit(1)
	}
}

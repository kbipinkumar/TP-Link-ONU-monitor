package cli

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/charmbracelet/huh"
	"github.com/kbipinkumar/TP-Link-ONU-monitor/internal/scraper"
	"gopkg.in/ini.v1"
)

func ensureConfig(configPath string) error {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		file, err := os.Create(configPath)
		if err != nil {
			return err
		}
		file.Close()
	}
	return nil
}

func RunInteractiveSetup(configPath string) {
	if err := ensureConfig(configPath); err != nil {
		log.Fatalf("Failed to create config file: %v", err)
	}

	cfg, err := ini.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config file: %v", err)
	}

	// Load existing values as defaults
	config, err := scraper.LoadConfig(configPath)
	if err != nil {
		config = &scraper.Config{}
		config.ONU.IP = "192.168.1.1"
		config.ONU.Username = "user"
		config.MQTT.Port = 1883
	}
	if config.MQTT.Port <= 0 || config.MQTT.Port > 65535 {
		config.MQTT.Port = 1883
	}

	var webUIUser, webUIPass string
	var onuIP, onuUser, onuPass string
	var enableMQTT bool
	var mqttBroker, mqttTopic, mqttClientID, mqttUser, mqttPass string
	var enableInflux bool
	var influxURL, influxToken, influxOrg, influxBucket string

	mqttPortStr := fmt.Sprintf("%d", config.MQTT.Port)
	enableMQTT = config.MQTT.Enable
	enableInflux = config.INFLUXDB.Enable

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().Title("ONU Monitor Configuration").Description("Welcome to the interactive setup wizard!"),
		),
		huh.NewGroup(
			huh.NewInput().Title("Web UI Username").Value(&webUIUser).Description("Leave blank to keep unchanged").EchoMode(huh.EchoModeNormal),
			huh.NewInput().Title("Web UI Password").Value(&webUIPass).Description("Leave blank to keep unchanged").EchoMode(huh.EchoModePassword),
		),
		huh.NewGroup(
			huh.NewInput().Title("ONU IP Address").Value(&onuIP).Placeholder(config.ONU.IP).Description("Default: " + config.ONU.IP),
			huh.NewInput().Title("ONU Login Username").Value(&onuUser).Placeholder(config.ONU.Username).Description("Usually 'user' or 'admin'"),
			huh.NewInput().Title("ONU Login Password").Value(&onuPass).Description("Leave blank to keep unchanged").EchoMode(huh.EchoModePassword),
		),
		huh.NewGroup(
			huh.NewConfirm().Title("Enable MQTT Publishing?").Value(&enableMQTT),
		),
		huh.NewGroup(
			huh.NewInput().Title("MQTT Broker (IP/Hostname)").Value(&mqttBroker).Placeholder(config.MQTT.Broker),
			huh.NewInput().Title("MQTT Port").Value(&mqttPortStr).Placeholder(mqttPortStr).Validate(func(s string) error {
				port, err := strconv.Atoi(s)
				if err != nil || port < 1 || port > 65535 {
					return fmt.Errorf("port must be an integer between 1 and 65535")
				}
				return nil
			}),
			huh.NewInput().Title("MQTT Base Topic").Value(&mqttTopic).Placeholder(config.MQTT.Topic),
			huh.NewInput().Title("MQTT Client ID").Value(&mqttClientID).Placeholder(config.MQTT.ClientID),
			huh.NewInput().Title("MQTT Username (Optional)").Value(&mqttUser).Placeholder(config.MQTT.User),
			huh.NewInput().Title("MQTT Password (Optional)").Value(&mqttPass).EchoMode(huh.EchoModePassword),
		).WithHideFunc(func() bool { return !enableMQTT }),
		huh.NewGroup(
			huh.NewConfirm().Title("Enable InfluxDB Publishing?").Value(&enableInflux),
		),
		huh.NewGroup(
			huh.NewInput().Title("InfluxDB Server URL").Value(&influxURL).Placeholder(config.INFLUXDB.URL),
			huh.NewInput().Title("InfluxDB API Token").Value(&influxToken).EchoMode(huh.EchoModePassword),
			huh.NewInput().Title("InfluxDB Organization").Value(&influxOrg).Placeholder(config.INFLUXDB.Org),
			huh.NewInput().Title("InfluxDB Bucket").Value(&influxBucket).Placeholder(config.INFLUXDB.Bucket),
		).WithHideFunc(func() bool { return !enableInflux }),
	)

	err = form.Run()
	if err != nil {
		fmt.Printf("Setup cancelled or failed: %v\n", err)
		return
	}

	// Update WebUI
	existingWebUIUser := cfg.Section("WEBUI").Key("USERNAME").String()
	
	if webUIUser != "" {
		cfg.Section("WEBUI").Key("USERNAME").SetValue(webUIUser)
	}

	finalWebUIUser := webUIUser
	if finalWebUIUser == "" {
		finalWebUIUser = existingWebUIUser
	}

	if webUIPass != "" {
		if finalWebUIUser == "" {
			fmt.Println("Warning: Cannot set a Web UI password without a username. Password change ignored.")
		} else {
			cfg.Section("WEBUI").Key("PASSWORD").SetValue(webUIPass)
		}
	}

	// Update ONU
	if onuIP != "" {
		cfg.Section("ONU").Key("IP").SetValue(onuIP)
	} else if !cfg.Section("ONU").HasKey("IP") {
		cfg.Section("ONU").Key("IP").SetValue(config.ONU.IP)
	}
	
	if onuUser != "" {
		cfg.Section("ONU").Key("USERNAME").SetValue(onuUser)
	} else if !cfg.Section("ONU").HasKey("USERNAME") {
		cfg.Section("ONU").Key("USERNAME").SetValue(config.ONU.Username)
	}
	
	if onuPass != "" {
		cfg.Section("ONU").Key("PASSWORD").SetValue(onuPass)
	}

	// Update MQTT
	if mqttPortStr != "" {
		cfg.Section("MQTT").Key("PORT").SetValue(mqttPortStr)
	}
	if enableMQTT {
		cfg.Section("MQTT").Key("ENABLE").SetValue("True")
		if mqttBroker != "" { cfg.Section("MQTT").Key("BROKER").SetValue(mqttBroker) }
		if mqttTopic != "" { cfg.Section("MQTT").Key("TOPIC").SetValue(mqttTopic) }
		if mqttClientID != "" { cfg.Section("MQTT").Key("CLIENT_ID").SetValue(mqttClientID) }
		if mqttUser != "" { cfg.Section("MQTT").Key("USER").SetValue(mqttUser) }
		if mqttPass != "" { cfg.Section("MQTT").Key("PASSWORD").SetValue(mqttPass) }
	} else {
		cfg.Section("MQTT").Key("ENABLE").SetValue("False")
	}

	// Update InfluxDB
	if enableInflux {
		cfg.Section("INFLUXDB").Key("ENABLE").SetValue("True")
		if influxURL != "" { cfg.Section("INFLUXDB").Key("URL").SetValue(influxURL) }
		if influxToken != "" { cfg.Section("INFLUXDB").Key("TOKEN").SetValue(influxToken) }
		if influxOrg != "" { cfg.Section("INFLUXDB").Key("ORG").SetValue(influxOrg) }
		if influxBucket != "" { cfg.Section("INFLUXDB").Key("BUCKET").SetValue(influxBucket) }
	} else {
		cfg.Section("INFLUXDB").Key("ENABLE").SetValue("False")
	}

	fmt.Println("\nTesting connection to ONU...")
	
	testCfg := &scraper.Config{}
	testCfg.ONU.IP = onuIP
	if testCfg.ONU.IP == "" {
		testCfg.ONU.IP = config.ONU.IP
	}
	testCfg.ONU.Username = onuUser
	if testCfg.ONU.Username == "" {
		testCfg.ONU.Username = config.ONU.Username
	}
	testCfg.ONU.Password = onuPass
	if testCfg.ONU.Password == "" {
		testCfg.ONU.Password = cfg.Section("ONU").Key("PASSWORD").String()
	}

	stats, err := scraper.GetGPONStats(testCfg)
	if err != nil {
		fmt.Printf("❌ Connection Test Failed: %v\n", err)
		fmt.Println("Configuration NOT saved. Please run 'onu-monitor setup' again with correct credentials.")
		os.Exit(1)
	}

	fmt.Println("✅ Success! Connected to ONU.")
	fmt.Printf("📊 RX Power: %.2f dBm | TX Power: %.2f dBm | Temp: %.2f °C\n\n", stats.RxPowerDBm, stats.TxPowerDBm, stats.TemperatureC)

	if err := cfg.SaveTo(configPath); err != nil {
		log.Fatalf("Failed to save config: %v", err)
	}

	fmt.Println("\nConfiguration saved successfully to", configPath)
	fmt.Println("Please restart the monitoring service to apply the changes:")
	fmt.Println("  sudo systemctl restart onu_monitor.timer")
}

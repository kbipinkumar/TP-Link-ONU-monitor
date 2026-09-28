//go:build live
package scraper

import (
	"os"
	"strings"
	"testing"
	"time"
)

func loadEnvConfig(t *testing.T) *Config {
	data, err := os.ReadFile("../../onu.env")
	if err != nil {
		t.Skip("onu.env not found, skipping live tests")
	}
	cfg := &Config{}
	cfg.ONU.IP = "192.168.1.1"
	cfg.ONU.Username = "user"
	
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			val := strings.Trim(parts[1], `"'`)
			switch parts[0] {
			case "ONU_IP":
				cfg.ONU.IP = val
			case "ONU_USERNAME":
				cfg.ONU.Username = val
			case "ONU_PASSWORD":
				cfg.ONU.Password = val
			}
		}
	}
	if cfg.ONU.Password == "" {
		t.Skip("ONU_PASSWORD empty in onu.env")
	}
	return cfg
}

func TestLive_Task1_SerialNumber(t *testing.T) {
	cfg := loadEnvConfig(t)

	// Scrape 1
	stats1, err := GetGPONStats(cfg)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "timeout") {
			t.Skipf("ONU unreachable: %v", err)
		}
		t.Fatalf("Scrape 1 failed: %v", err)
	}
	
	if stats1.SerialNumber == "" {
		t.Fatalf("Scrape 1 returned empty serial number")
	}
	t.Logf("Scrape 1 serial: %s", maskSerial(stats1.SerialNumber))

	time.Sleep(3 * time.Second)

	// Scrape 2
	stats2, err := GetGPONStats(cfg)
	if err != nil {
		t.Fatalf("Scrape 2 failed: %v", err)
	}
	if stats2.SerialNumber == "" {
		t.Fatalf("Scrape 2 returned empty serial number")
	}
	t.Logf("Scrape 2 serial: %s", maskSerial(stats2.SerialNumber))
	
	if stats1.SerialNumber != stats2.SerialNumber {
		t.Errorf("Serial number changed between scrapes! 1: %s, 2: %s", stats1.SerialNumber, stats2.SerialNumber)
	}
}

func maskSerial(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

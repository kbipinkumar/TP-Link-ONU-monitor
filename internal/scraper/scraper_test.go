package scraper

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveIdentity(t *testing.T) {
	stats1 := &GPONStats{SerialNumber: "12345"}
	status1 := &SystemStatus{}
	ResolveIdentity(stats1, status1)
	if stats1.SerialNumber != "12345" {
		t.Errorf("Expected 12345, got %s", stats1.SerialNumber)
	}
	if status1.SerialNumber != "12345" {
		t.Errorf("Expected status to be updated to 12345, got %s", status1.SerialNumber)
	}

	stats2 := &GPONStats{SerialNumber: ""}
	status2 := &SystemStatus{SerialNumber: "12345"}
	ResolveIdentity(stats2, status2)
	if stats2.SerialNumber != "12345" {
		t.Errorf("Expected 12345 (retained), got %s", stats2.SerialNumber)
	}

	stats3 := &GPONStats{SerialNumber: ""}
	status3 := &SystemStatus{SerialNumber: ""}
	ResolveIdentity(stats3, status3)
	if stats3.SerialNumber != "" {
		t.Errorf("Expected empty, got %s", stats3.SerialNumber)
	}
}

func TestTask2_OptionalFields(t *testing.T) {
	stats := &GPONStats{}
	b, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "cpu_usage") {
		t.Errorf("expected cpu_usage to be omitted, got %s", s)
	}
	if strings.Contains(s, "mem_usage") {
		t.Errorf("expected mem_usage to be omitted, got %s", s)
	}
	if strings.Contains(s, "uptime") {
		t.Errorf("expected uptime to be omitted, got %s", s)
	}
}

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

func TestBuildDiscoveryPayloads(t *testing.T) {
	payloads := BuildDiscoveryPayloads("test_serial", "test_model", "test/topic/state", 900)
	
	if len(payloads) != 12 {
		t.Errorf("Expected 12 payloads, got %d", len(payloads))
	}
	
	for topic, payload := range payloads {
		payloadMap := payload.(map[string]interface{})
		if payloadMap["state_topic"] != "test/topic/state" {
			t.Errorf("Expected state topic test/topic/state, got %v", payloadMap["state_topic"])
		}
		if payloadMap["expire_after"] != 900 {
			t.Errorf("Expected expire_after 900, got %v", payloadMap["expire_after"])
		}
		if strings.Contains(topic, "cpu_usage") {
			if payloadMap["state_class"] != "measurement" {
				t.Errorf("Expected cpu_usage state_class measurement, got %v", payloadMap["state_class"])
			}
		}
		if strings.Contains(topic, "serial_number") {
			if _, ok := payloadMap["state_class"]; ok {
				t.Errorf("Expected serial_number to not have state_class, but it did")
			}
		}
	}
}

func TestSanitizeSerial(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"12345", "12345"},
		{"aB-c_d", "aB-c_d"},
		{"a+b#c/d e", "abcde"},
		{"", "unknown"},
		{"+#/", "unknown"},
		{"   ", "unknown"},
	}

	for _, tt := range tests {
		got := SanitizeSerial(tt.input)
		if got != tt.expected {
			t.Errorf("SanitizeSerial(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

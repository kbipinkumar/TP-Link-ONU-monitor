package scraper

import (
	"testing"
)

func TestResolveIdentity(t *testing.T) {
	// First call has a serial, status has none
	stats1 := &GPONStats{SerialNumber: "12345"}
	status1 := &SystemStatus{}
	ResolveIdentity(stats1, status1)
	if stats1.SerialNumber != "12345" {
		t.Errorf("Expected 12345, got %s", stats1.SerialNumber)
	}
	if status1.SerialNumber != "12345" {
		t.Errorf("Expected status to be updated to 12345, got %s", status1.SerialNumber)
	}

	// Second call has no serial, but status has the one from previous call
	stats2 := &GPONStats{SerialNumber: ""}
	status2 := &SystemStatus{SerialNumber: "12345"}
	ResolveIdentity(stats2, status2)
	if stats2.SerialNumber != "12345" {
		t.Errorf("Expected 12345 (retained), got %s", stats2.SerialNumber)
	}

	// Never known
	stats3 := &GPONStats{SerialNumber: ""}
	status3 := &SystemStatus{SerialNumber: ""}
	ResolveIdentity(stats3, status3)
	if stats3.SerialNumber != "" {
		t.Errorf("Expected empty, got %s", stats3.SerialNumber)
	}
}

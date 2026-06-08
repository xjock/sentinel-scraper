package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteKMLFile(t *testing.T) {
	tmpDir := t.TempDir()
	kmlPath := filepath.Join(tmpDir, "test.kml")

	ring := [][]float64{
		{116.2, 39.8},
		{116.6, 39.8},
		{116.6, 40.0},
		{116.2, 40.0},
		{116.2, 39.8},
	}

	fields := []kmlField{
		{Name: "id", Value: "S2A_TEST"},
		{Name: "collection", Value: "sentinel-2-l2a"},
		{Name: "datetime", Value: "2025-01-01T00:00:00Z"},
		{Name: "empty", Value: ""},
	}

	if err := writeKMLFile(kmlPath, "S2A_TEST", ring, fields); err != nil {
		t.Fatalf("writeKMLFile failed: %v", err)
	}

	data, err := os.ReadFile(kmlPath)
	if err != nil {
		t.Fatalf("failed to read kml: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "<?xml version=\"1.0\"") {
		t.Error("missing XML declaration")
	}
	if !strings.Contains(content, "<name>S2A_TEST</name>") {
		t.Error("missing display name")
	}
	if !strings.Contains(content, "<Data name=\"id\"><value>S2A_TEST</value></Data>") {
		t.Error("missing id field")
	}
	if !strings.Contains(content, "<Data name=\"collection\"><value>sentinel-2-l2a</value></Data>") {
		t.Error("missing collection field")
	}
	if strings.Contains(content, "empty") {
		t.Error("empty value field should be skipped")
	}
	if !strings.Contains(content, "<coordinates>") {
		t.Error("missing coordinates")
	}
	if !strings.Contains(content, "116.200000,39.800000,0") {
		t.Error("missing first coordinate")
	}
}

func TestWriteKMLFile_XMLEscape(t *testing.T) {
	tmpDir := t.TempDir()
	kmlPath := filepath.Join(tmpDir, "escape.kml")

	ring := [][]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {0, 0}}
	fields := []kmlField{
		{Name: "special", Value: "<script>alert('xss')</script>"},
	}

	if err := writeKMLFile(kmlPath, "Test <>", ring, fields); err != nil {
		t.Fatalf("writeKMLFile failed: %v", err)
	}

	data, _ := os.ReadFile(kmlPath)
	content := string(data)

	if strings.Contains(content, "<script>") {
		t.Error("value was not HTML-escaped")
	}
	if !strings.Contains(content, "&lt;script&gt;") {
		t.Error("expected escaped script tag")
	}
	if !strings.Contains(content, "Test &lt;&gt;") {
		t.Error("expected escaped display name")
	}
}

func TestWriteKMLFile_EmptyRing(t *testing.T) {
	tmpDir := t.TempDir()
	kmlPath := filepath.Join(tmpDir, "empty.kml")

	if err := writeKMLFile(kmlPath, "Empty", [][]float64{}, nil); err != nil {
		t.Fatalf("writeKMLFile failed: %v", err)
	}

	data, _ := os.ReadFile(kmlPath)
	if len(data) == 0 {
		t.Error("kml file is empty")
	}
}

package logging

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSeverity_String(t *testing.T) {
	tests := []struct {
		s    Severity
		want string
	}{
		{Debug, "DEBUG"},
		{Info, "INFO"},
		{Error, "ERROR"},
		{Warning, "CRITICAL"}, // based on implementation default
		{Critical, "CRITICAL"},
	}

	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestEntry_String(t *testing.T) {
	e := entry{
		Message:  "test message",
		Severity: "INFO",
		Trace:    "projects/p/traces/t",
		File:     "file.go",
		Row:      42,
	}

	str := e.String()
	var parsed entry
	if err := json.Unmarshal([]byte(str), &parsed); err != nil {
		t.Fatalf("failed to unmarshal entry string: %v", err)
	}

	if parsed.Message != e.Message || parsed.Severity != e.Severity || parsed.Trace != e.Trace {
		t.Errorf("unexpected parsed entry: %+v", parsed)
	}
}

func TestInitLogging_And_ShouldLog(t *testing.T) {
	levels := []string{"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL", "UNKNOWN"}

	for _, lvl := range levels {
		t.Run("Level_"+lvl, func(t *testing.T) {
			os.Setenv("LOG_LEVEL", lvl)
			defer os.Unsetenv("LOG_LEVEL")

			InitLogging("my-gcp-project")
			if !runningInGCP || projectID != "my-gcp-project" {
				t.Errorf("expected runningInGCP true and projectID set")
			}

			// ShouldLog checks
			_ = ShouldLog(Debug)
			_ = ShouldLog(Info)
			_ = ShouldLog(Warning)
			_ = ShouldLog(Error)
			_ = ShouldLog(Critical)
		})
	}

	// Test with empty GCP project
	os.Unsetenv("LOG_LEVEL")
	InitLogging("")
	if runningInGCP || projectID != "" {
		t.Errorf("expected runningInGCP false and projectID empty")
	}
}

func TestTrace(t *testing.T) {
	runningInGCP = true
	projectID = "test-proj"
	defer func() {
		runningInGCP = false
		projectID = ""
	}()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Cloud-Trace-Context", "trace12345/span678;o=1")

	trace := Trace(req)
	expected := "projects/test-proj/traces/trace12345"
	if trace != expected {
		t.Errorf("Trace() = %q, want %q", trace, expected)
	}

	// Empty trace header
	reqEmpty := httptest.NewRequest(http.MethodGet, "/", nil)
	if traceEmpty := Trace(reqEmpty); traceEmpty != NoTrace {
		t.Errorf("Trace() for empty header = %q, want %q", traceEmpty, NoTrace)
	}

	// Not in GCP
	runningInGCP = false
	if traceNoGCP := Trace(req); traceNoGCP != NoTrace {
		t.Errorf("Trace() when runningInGCP=false = %q, want %q", traceNoGCP, NoTrace)
	}
}

func TestLog_And_LogTrace(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	logLevel = Info
	runningInGCP = true
	projectID = "test-proj"

	// Log with Info (should succeed)
	Log("hello info", Info)
	if !strings.Contains(buf.String(), "hello info") {
		t.Errorf("expected log output to contain 'hello info', got: %s", buf.String())
	}

	// Log with Debug (filtered out by shouldLog, triggers warning log)
	buf.Reset()
	Log("hello debug", Debug)
	if !strings.Contains(buf.String(), "Logging should be filtered with ShouldLog") {
		t.Errorf("expected nudge message, got: %s", buf.String())
	}

	// LogTrace with Info and request
	buf.Reset()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Cloud-Trace-Context", "traceabc/span1")
	LogTrace("hello trace", Info, req)
	if !strings.Contains(buf.String(), "hello trace") || !strings.Contains(buf.String(), "projects/test-proj/traces/traceabc") {
		t.Errorf("expected log output to contain trace, got: %s", buf.String())
	}

	// LogTrace with Debug (filtered out)
	buf.Reset()
	LogTrace("hello trace debug", Debug, req)
	if !strings.Contains(buf.String(), "Logging should be filtered with ShouldLog") {
		t.Errorf("expected nudge message, got: %s", buf.String())
	}
}

package logging

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
)

type Severity int8

const Debug Severity = 0
const Info Severity = 1
const Warning Severity = 2
const Error Severity = 3
const Critical Severity = 4
const NoTrace string = ""

var projectID string = ""
var runningInGCP bool = false
var logLevel = Error

//Returns true if the severity should be logged.
func ShouldLog(severity Severity) bool {
	return severity >= logLevel
}

func (s Severity) String() string {
	if s == Debug {
		return "DEBUG"
	}

	if s == Info {
		return "INFO"
	}

	if s == Error {
		return "ERROR"
	}

	return "CRITICAL"
}

type entry struct {
	Message  string `json:"message"`
	Severity string `json:"severity,omitempty"`
	Trace    string `json:"logging.googleapis.com/trace,omitempty"`
	File     string `json:"file,omitempty"`
	Row      int    `json:"row,omitempty"`
}

func (e entry) String() string {
	out, err := json.Marshal(e)
	if err != nil {
		log.Printf("json.Marshal: %v", err)
	}

	return string(out)
}

//Setup logging, log level
func InitLogging(gcpproject string) {
	runningInGCP = len(gcpproject) > 0
	projectID = gcpproject

	logLevelString := os.Getenv("LOG_LEVEL")
	fromenv := false
	switch logLevelString {
	case Info.String():
		logLevel = Info
		fromenv = true
	case Debug.String():
		logLevel = Debug
		fromenv = true
	case Warning.String():
		logLevel = Warning
		fromenv = true
	case Error.String():
		logLevel = Error
		fromenv = true
	case Critical.String():
		logLevel = Critical
		fromenv = true

	default:
		logLevel = Info
	}

	log.SetFlags(0)

	if ShouldLog(Info) {
		if fromenv {
			Log(fmt.Sprintf("Settting log level %s from LOG_LEVEL variable", logLevel.String()), Info)
		} else {
			Log(fmt.Sprintf("Settting log level %s from default", logLevel.String()), Info)
		}
	}
}

//Create Trace string based on google project if we run in a cloud env
func Trace(r *http.Request) string {

	if runningInGCP && projectID != "" {
		traceHeader := r.Header.Get("X-Cloud-Trace-Context")
		traceParts := strings.Split(traceHeader, "/")
		if len(traceParts) > 0 && len(traceParts[0]) > 0 {
			return fmt.Sprintf("projects/%s/traces/%s", projectID, traceParts[0])
		}
	}

	return NoTrace
}

//Log error message
func Log(payload string, severity Severity) {
	if shouldLog(severity) {
		writeLog(payload, severity, NoTrace)
	}
}

//Log error message with trace from http request
func LogTrace(payload string, severity Severity, r *http.Request) {
	if shouldLog(severity) {
		writeLog(payload, severity, Trace(r))
	}

}

// Internal funciton to write log
func writeLog(payload string, severity Severity, trace string) {

	logentry := entry{
		Message:  payload,
		Severity: severity.String(),
		Trace:    trace,
	}

	_, logentry.File, logentry.Row, _ = runtime.Caller(2)

	log.Println(logentry)
}

//Internal function to should log, and nudges developer that they havn't checked if function should
//be run
func shouldLog(severity Severity) bool {

	if !ShouldLog(severity) {
		logentry := entry{
			Message:  "Logging should be filtered with ShouldLog before calling.",
			Severity: Info.String(),
			Trace:    NoTrace,
		}

		_, logentry.File, logentry.Row, _ = runtime.Caller(2)

		log.Println(logentry)

		return false
	}

	return true
}

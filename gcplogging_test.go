package goutils

import (
	"testing"

	"github.com/tingdahl/goutils/logging"
)

func TestInitLogging(t *testing.T) {
	logging.InitLogging(GCPProject())
}

package goutils

import (
	"testing"

	"github.com/tingdahl/goutils/logging"
)

func TestMain(m *testing.M) {
	InitGCPEnvironment("dummy")
	logging.InitLogging(GCPProject())
	m.Run()
}

func TestGetOtap(t *testing.T) {
	//It should not crash
	otap := GetOtap()
	if otap != "dev" {
		t.Error("GetOtap did not return dev")
	}
}

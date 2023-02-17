package goutils

import (
	"testing"

	logging "github.com/tingdahl/gcplogging"
)

func TestMain(m *testing.M) {
	logging.InitGCPEnvironment("dummy")
	logging.InitLogging(logging.GCPProject())
	m.Run()
}

func TestGetOtap(t *testing.T) {
	//It should not crash
	otap := GetOtap()
	if otap != "dev" {
		t.Error("GetOtap did not return dev")
	}
}

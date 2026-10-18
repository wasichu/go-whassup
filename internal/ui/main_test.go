package ui

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	readDwell = 5 * time.Millisecond // so read timers fire within the harness's waits
	os.Exit(m.Run())
}

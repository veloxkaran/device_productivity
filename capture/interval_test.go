package capture

import (
	"os"
	"testing"
	"time"
)

func TestIntervalIsPersistedAndClamped(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(dir)
	os.MkdirAll("data", 0755)

	if got := loadSavedInterval(); got != 0 {
		t.Fatalf("no file: got %s, want 0", got)
	}
	SetInterval(90 * time.Second)
	if got := CurrentInterval(); got != 90*time.Second {
		t.Fatalf("current = %s", got)
	}
	if got := loadSavedInterval(); got != 90*time.Second {
		t.Fatalf("saved = %s, want 1m30s", got)
	}
	SetInterval(5 * time.Second)
	if got := CurrentInterval(); got != minInterval {
		t.Fatalf("below min: %s", got)
	}
	SetInterval(3 * time.Hour)
	if got := CurrentInterval(); got != maxInterval {
		t.Fatalf("above max: %s", got)
	}
	os.WriteFile(intervalFile, []byte("abc"), 0644)
	if got := loadSavedInterval(); got != 0 {
		t.Fatalf("garbage file: %s", got)
	}
}

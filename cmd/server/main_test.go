package main

import (
	"os"
	"testing"
	"time"
)

func TestEnvironmentParsers(t *testing.T) {
	t.Setenv("TEST_INT", "12")
	value, err := envInt("TEST_INT", 1, 1, 20)
	if err != nil || value != 12 {
		t.Fatalf("value=%d error=%v", value, err)
	}
	t.Setenv("TEST_INT", "999")
	if _, err := envInt("TEST_INT", 1, 1, 20); err == nil {
		t.Fatal("out-of-range integer accepted")
	}
	t.Setenv("TEST_DURATION", "3s")
	duration, err := envDuration("TEST_DURATION", time.Second, time.Second, time.Minute)
	if err != nil || duration != 3*time.Second {
		t.Fatalf("duration=%s error=%v", duration, err)
	}
	_ = os.Unsetenv("TEST_DURATION")
}

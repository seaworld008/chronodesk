package services

import (
    "errors"
    "testing"
    "time"
)

func TestWebhookRetryBackoff(t *testing.T) {
    attempts := 0
    send := func() error {
        attempts++
        if attempts < 3 {
            return errors.New("fail")
        }
        return nil
    }

    err := runWithRetry(send, 3, 10*time.Millisecond)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if attempts != 3 {
        t.Fatalf("expected 3 attempts, got %d", attempts)
    }
}

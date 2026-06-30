package notify

import (
	"errors"
	"strings"
	"testing"
)

func TestDeliveryErrorFormattingAndUnwrap(t *testing.T) {
	baseErr := errors.New("boom")
	err := &DeliveryError{
		Provider:   "pushover",
		StatusCode: 400,
		Permanent:  true,
		Err:        baseErr,
	}
	if !strings.Contains(err.Error(), "HTTP status 400") {
		t.Fatalf("expected HTTP status in error, got %q", err.Error())
	}
	if !errors.Is(err, baseErr) {
		t.Fatal("expected wrapped base error")
	}
	if !IsPermanent(err) {
		t.Fatal("expected permanent delivery error")
	}

	err.StatusCode = 0
	if !strings.Contains(err.Error(), "pushover delivery failed: boom") {
		t.Fatalf("expected provider error, got %q", err.Error())
	}
}

func TestNilDeliveryError(t *testing.T) {
	var err *DeliveryError
	if err.Error() != "" {
		t.Fatalf("expected empty nil error string, got %q", err.Error())
	}
	if err.Unwrap() != nil {
		t.Fatal("expected nil unwrap")
	}
}

func TestIsPermanentReturnsFalseForNonDeliveryError(t *testing.T) {
	if IsPermanent(errors.New("plain")) {
		t.Fatal("plain errors should not be permanent")
	}
}

func TestLoggerOrDiscard(t *testing.T) {
	logger := loggerOrDiscard(nil)
	if logger == nil {
		t.Fatal("expected fallback logger")
	}
	if got := loggerOrDiscard(logger); got != logger {
		t.Fatal("expected existing logger to be returned")
	}
}

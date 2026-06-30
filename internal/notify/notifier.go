package notify

import (
	"context"
	"errors"
	"fmt"
)

type Message struct {
	Title string
	Body  string
}

type Notifier interface {
	Name() string
	Notify(ctx context.Context, message Message) error
}

type DeliveryError struct {
	Provider   string
	StatusCode int
	Permanent  bool
	Err        error
}

func (e *DeliveryError) Error() string {
	if e == nil {
		return ""
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("%s delivery failed with HTTP status %d: %v", e.Provider, e.StatusCode, e.Err)
	}
	return fmt.Sprintf("%s delivery failed: %v", e.Provider, e.Err)
}

func (e *DeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func IsPermanent(err error) bool {
	var delivery *DeliveryError
	if errors.As(err, &delivery) {
		return delivery.Permanent
	}
	return false
}

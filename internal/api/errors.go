package api

import "fmt"

// RequestError represents an HTTP request error
type RequestError struct {
	Endpoint      string
	StatusCode    int
	Message       string
	OriginalError error
}

func (e *RequestError) Error() string {
	if e.OriginalError != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.OriginalError)
	}
	return e.Message
}

// SubscriptionError represents a subscription tier error
type SubscriptionError struct {
	Endpoint string
	Message  string
}

func (e *SubscriptionError) Error() string {
	return e.Message
}

// RateLimitError represents a rate limit error
type RateLimitError struct {
	Endpoint  string
	Message   string
	RetryAfter string
}

func (e *RateLimitError) Error() string {
	return e.Message
}

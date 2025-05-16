package client

import (
	"fmt"
	"io"
	"sync"
)

// CaptureReader
// Captures the reader for processing instead of performing a JSON marshal operation
type CaptureReader interface {
	Capture(reader io.Reader) error
}

// CodedResponse
// An object implementing this can track the response code from server / client. Complements kitDefaults.StatusCoder
type CodedResponse interface {
	NewCode(code int)
}

// ErredResponse
// An object implementing this can track the error from the server / client. Complements error interface
type ErredResponse interface {
	NewError(code int, format string, vars ...interface{})
}

// BasicResponse
//
// When embedded into a Response object, this wil provide basic functionality
type BasicResponse struct {
	code int
}

// StatusCode
//
// Returns the status code set by the NewError method
func (b BasicResponse) StatusCode() int {
	return b.code
}

func (b *BasicResponse) NewCode(code int) {
	b.code = code
}

// ErrorResponse
//
// When embedded into a Response object, this wil provide error handling functionality
type ErrorResponse struct {
	code      int
	errString string
}

func (b *ErrorResponse) NewCode(code int) {
	b.code = code
}

func (b ErrorResponse) StatusCode() int {
	return b.code
}

// Failed
//
// Implements kitDefaults.Failer
func (b ErrorResponse) Failed() error {
	if b.errString != "" {
		return b
	}
	return nil
}

// NewError
//
// Use this function when it is necessary to indicate an error result for business logic
func (b *ErrorResponse) NewError(code int, format string, vars ...interface{}) {
	b.code = code
	b.errString = fmt.Sprintf(format, vars...)
}

// Error
//
// Implements error interface
func (b ErrorResponse) Error() string {
	return b.errString
}

type ExtendedLog interface {
	GetAll() map[string]interface{}
}

// ExpandedLogging
//
// Added to a response, should enable additional request-scoped log values
type ExpandedLogging struct {
	lvalues map[string]interface{}
	lock    sync.Mutex
}

// Log
//
// create a new log entry to be traversed later
func (l *ExpandedLogging) Log(values ...interface{}) {
	l.lock.Lock()
	defer l.lock.Unlock()
	if l.lvalues == nil {
		l.lvalues = make(map[string]interface{})
	}
	for i := 0; i < len(values); i += 2 {
		if i+1 >= len(values) {
			l.lvalues[fmt.Sprintf("%s", values[i])] = nil
		} else {
			l.lvalues[fmt.Sprintf("%s", values[i])] = values[i+1]
		}
	}
}

// GetAll
//
// creates defensive copy of the underlying map
func (l *ExpandedLogging) GetAll() map[string]interface{} {
	l.lock.Lock()
	defer l.lock.Unlock()
	result := make(map[string]interface{}, len(l.lvalues))
	for k, v := range l.lvalues {
		result[k] = v
	}
	return result
}

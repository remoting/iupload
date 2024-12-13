package fileserver

import (
	"errors"
	"fmt"
	weakrand "math/rand"
	"path"
	"runtime"
	"strings"
)

func Error(statusCode int, err error) HandlerError {
	const idLen = 9
	var he HandlerError
	if errors.As(err, &he) {
		if he.ID == "" {
			he.ID = randString(idLen, true)
		}
		if he.Trace == "" {
			he.Trace = trace()
		}
		if he.StatusCode == 0 {
			he.StatusCode = statusCode
		}
		return he
	}
	return HandlerError{
		ID:         randString(idLen, true),
		StatusCode: statusCode,
		Err:        err,
		Trace:      trace(),
	}
}

// HandlerError is a serializable representation of
// an error from within an HTTP handler.
type HandlerError struct {
	Err        error // the original error value and message
	StatusCode int   // the HTTP status code to associate with this error

	ID    string // generated; for identifying this error in logs
	Trace string // produced from call stack
}

func (e HandlerError) Error() string {
	var s string
	if e.ID != "" {
		s += fmt.Sprintf("{id=%s}", e.ID)
	}
	if e.Trace != "" {
		s += " " + e.Trace
	}
	if e.StatusCode != 0 {
		s += fmt.Sprintf(": HTTP %d", e.StatusCode)
	}
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return strings.TrimSpace(s)
}

// Unwrap returns the underlying error value. See the `errors` package for info.
func (e HandlerError) Unwrap() error { return e.Err }

// randString returns a string of n random characters.
// It is not even remotely secure OR a proper distribution.
// But it's good enough for some things. It excludes certain
// confusing characters like I, l, 1, 0, O, etc. If sameCase
// is true, then uppercase letters are excluded.
func randString(n int, sameCase bool) string {
	if n <= 0 {
		return ""
	}
	dict := []byte("abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRTUVWXY23456789")
	if sameCase {
		dict = []byte("abcdefghijkmnpqrstuvwxyz0123456789")
	}
	b := make([]byte, n)
	for i := range b {
		//nolint:gosec
		b[i] = dict[weakrand.Int63()%int64(len(dict))]
	}
	return string(b)
}

func trace() string {
	if pc, file, line, ok := runtime.Caller(2); ok {
		filename := path.Base(file)
		pkgAndFuncName := path.Base(runtime.FuncForPC(pc).Name())
		return fmt.Sprintf("%s (%s:%d)", pkgAndFuncName, filename, line)
	}
	return ""
}

package middleware

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

const (
	transactionalResponseMemoryThreshold = 256 << 10
	transactionalResponseMaximumBytes    = 32 << 20
)

var (
	errTransactionalResponseTooLarge = errors.New(
		"response exceeds the transactional response limit",
	)
	errTransactionalResponseHijack = errors.New(
		"transactional responses cannot hijack the connection",
	)
)

// TransactionalResponseBuffer buffers the complete response until its owning
// database transaction has committed. Small JSON responses stay in memory;
// attachment-sized responses spill to an owner-only temporary file and remain
// bounded. Streaming/hijacking is deliberately unavailable on this boundary,
// because emitting bytes before COMMIT could return a false success.
type TransactionalResponseBuffer struct {
	underlying gin.ResponseWriter
	header     http.Header
	memory     bytes.Buffer
	spill      *os.File
	status     int
	size       int
	writeErr   error
}

var _ gin.ResponseWriter = (*TransactionalResponseBuffer)(nil)

// NewTransactionalResponseBuffer wraps a Gin response before any header or
// body has been emitted. Call Commit only after the owning transaction
// successfully commits, and always call Close to remove a possible spool.
func NewTransactionalResponseBuffer(
	underlying gin.ResponseWriter,
) (*TransactionalResponseBuffer, error) {
	if underlying == nil {
		return nil, errors.New("transactional response writer is required")
	}
	if underlying.Written() {
		return nil, errors.New(
			"transactional response must start before response output",
		)
	}
	return &TransactionalResponseBuffer{
		underlying: underlying,
		header:     underlying.Header().Clone(),
		status:     http.StatusOK,
		size:       -1,
	}, nil
}

func (response *TransactionalResponseBuffer) Header() http.Header {
	return response.header
}

func (response *TransactionalResponseBuffer) WriteHeader(status int) {
	if response.Written() || status <= 0 {
		return
	}
	response.status = status
}

func (response *TransactionalResponseBuffer) WriteHeaderNow() {
	if !response.Written() {
		response.size = 0
	}
}

func (response *TransactionalResponseBuffer) Write(payload []byte) (int, error) {
	response.WriteHeaderNow()
	if response.writeErr != nil {
		return 0, response.writeErr
	}
	if len(payload) > transactionalResponseMaximumBytes-response.size {
		response.writeErr = errTransactionalResponseTooLarge
		return 0, response.writeErr
	}
	if response.spill == nil &&
		response.size+len(payload) > transactionalResponseMemoryThreshold {
		spill, err := os.CreateTemp(
			"",
			"chronodesk-transactional-response-*",
		)
		if err != nil {
			response.writeErr = fmt.Errorf(
				"create transactional response spool: %w",
				err,
			)
			return 0, response.writeErr
		}
		response.spill = spill
		if response.memory.Len() > 0 {
			if _, err := response.spill.Write(response.memory.Bytes()); err != nil {
				response.writeErr = fmt.Errorf(
					"write transactional response spool: %w",
					err,
				)
				return 0, response.writeErr
			}
			response.memory.Reset()
		}
	}

	var (
		written int
		err     error
	)
	if response.spill != nil {
		written, err = response.spill.Write(payload)
	} else {
		written, err = response.memory.Write(payload)
	}
	response.size += written
	if err != nil {
		response.writeErr = fmt.Errorf("buffer transactional response: %w", err)
	}
	return written, response.writeErr
}

func (response *TransactionalResponseBuffer) WriteString(
	value string,
) (int, error) {
	return response.Write([]byte(value))
}

func (response *TransactionalResponseBuffer) Status() int {
	return response.status
}

func (response *TransactionalResponseBuffer) Size() int {
	return response.size
}

func (response *TransactionalResponseBuffer) Written() bool {
	return response.size >= 0
}

func (response *TransactionalResponseBuffer) Flush() {
	// A real flush would cross the transaction boundary. Mark the response as
	// started for Gin compatibility while retaining every byte until COMMIT.
	response.WriteHeaderNow()
}

func (response *TransactionalResponseBuffer) Hijack() (
	net.Conn,
	*bufio.ReadWriter,
	error,
) {
	return nil, nil, errTransactionalResponseHijack
}

func (response *TransactionalResponseBuffer) CloseNotify() <-chan bool {
	return response.underlying.CloseNotify()
}

func (response *TransactionalResponseBuffer) Pusher() http.Pusher {
	// HTTP/2 push is an observable response side effect and therefore cannot
	// occur before the database transaction commits.
	return nil
}

func (response *TransactionalResponseBuffer) Err() error {
	return response.writeErr
}

func (response *TransactionalResponseBuffer) Commit() error {
	if response.writeErr != nil {
		return response.writeErr
	}
	targetHeader := response.underlying.Header()
	for key := range targetHeader {
		targetHeader.Del(key)
	}
	for key, values := range response.header {
		for _, value := range values {
			targetHeader.Add(key, value)
		}
	}
	response.underlying.WriteHeader(response.status)
	if response.size <= 0 {
		response.underlying.WriteHeaderNow()
		return nil
	}

	var source io.Reader = bytes.NewReader(response.memory.Bytes())
	if response.spill != nil {
		if _, err := response.spill.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("rewind transactional response spool: %w", err)
		}
		source = response.spill
	}
	written, err := io.Copy(response.underlying, source)
	if err != nil {
		return fmt.Errorf("emit committed transactional response: %w", err)
	}
	if written != int64(response.size) {
		return fmt.Errorf(
			"emit committed transactional response: wrote %d of %d bytes",
			written,
			response.size,
		)
	}
	return nil
}

func (response *TransactionalResponseBuffer) Close() error {
	if response.spill == nil {
		return nil
	}
	spill := response.spill
	response.spill = nil
	name := spill.Name()
	closeErr := spill.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

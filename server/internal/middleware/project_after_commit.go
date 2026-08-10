package middleware

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const projectAfterCommitQueueContextKey = "project_after_commit"

const projectAfterCommitResponseMaximumBytes = 64 << 10

// ProjectAfterCommitResponse is a deliberately narrow conflict response that
// may be emitted only after the owning Project transaction commits. It is used
// when the committed domain outcome itself is a stable HTTP 409 problem.
type ProjectAfterCommitResponse struct {
	Status      int
	ContentType string
	Header      http.Header
	Body        []byte
}

type projectAfterCommitQueue struct {
	callbacks []func()
	response  *ProjectAfterCommitResponse
}

func InstallProjectAfterCommitQueue(c *gin.Context) error {
	if c == nil {
		return errors.New("project after-commit context is required")
	}
	if _, exists := c.Get(projectAfterCommitQueueContextKey); exists {
		return errors.New("project after-commit queue is already installed")
	}
	c.Set(projectAfterCommitQueueContextKey, &projectAfterCommitQueue{})
	return nil
}

func QueueProjectAfterCommit(
	c *gin.Context,
	callback func(),
) error {
	queue, err := projectAfterCommitQueueFromContext(c)
	if err != nil || callback == nil {
		if err != nil {
			return err
		}
		return errors.New("project after-commit callback is invalid")
	}
	queue.callbacks = append(queue.callbacks, callback)
	return nil
}

func QueueProjectAfterCommitResponse(
	c *gin.Context,
	response ProjectAfterCommitResponse,
) error {
	queue, err := projectAfterCommitQueueFromContext(c)
	if err != nil {
		return err
	}
	if response.Status != http.StatusConflict {
		return errors.New(
			"project after-commit response must be a conflict",
		)
	}
	if response.ContentType != "application/problem+json" {
		return errors.New(
			"project after-commit response must be a problem",
		)
	}
	if len(response.Body) > projectAfterCommitResponseMaximumBytes {
		return errors.New("project after-commit response is too large")
	}
	for key := range response.Header {
		if !strings.EqualFold(key, "ETag") {
			return errors.New(
				"project after-commit response header is not allowed",
			)
		}
	}
	if queue.response != nil {
		return errors.New(
			"project after-commit response is already queued",
		)
	}
	copied := ProjectAfterCommitResponse{
		Status:      response.Status,
		ContentType: response.ContentType,
		Header:      response.Header.Clone(),
		Body:        append([]byte(nil), response.Body...),
	}
	queue.response = &copied
	return nil
}

func HasProjectAfterCommitResponse(c *gin.Context) bool {
	queue, err := projectAfterCommitQueueFromContext(c)
	return err == nil && queue.response != nil
}

func ProjectAfterCommitQueueInstalled(c *gin.Context) bool {
	_, err := projectAfterCommitQueueFromContext(c)
	return err == nil
}

func RunProjectAfterCommitCallbacks(c *gin.Context) (callbackErr error) {
	queue, err := projectAfterCommitQueueFromContext(c)
	if err != nil {
		return err
	}
	callbacks := append([]func(){}, queue.callbacks...)
	queue.callbacks = nil
	for _, callback := range callbacks {
		func() {
			defer func() {
				if recover() != nil {
					callbackErr = errors.Join(
						callbackErr,
						errors.New(
							"project after-commit callback panicked",
						),
					)
				}
			}()
			callback()
		}()
	}
	return callbackErr
}

func EmitProjectAfterCommitResponse(c *gin.Context) (bool, error) {
	queue, err := projectAfterCommitQueueFromContext(c)
	if err != nil || queue.response == nil {
		return false, err
	}
	response := queue.response
	queue.response = nil
	for key, values := range response.Header {
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}
	contentType := response.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.WriteHeader(response.Status)
	written, writeErr := c.Writer.Write(response.Body)
	if writeErr != nil {
		return true, writeErr
	}
	if written != len(response.Body) {
		return true, io.ErrShortWrite
	}
	return true, nil
}

func projectAfterCommitQueueFromContext(
	c *gin.Context,
) (*projectAfterCommitQueue, error) {
	if c == nil {
		return nil, errors.New(
			"project after-commit context is required",
		)
	}
	value, exists := c.Get(projectAfterCommitQueueContextKey)
	queue, ok := value.(*projectAfterCommitQueue)
	if !exists || !ok || queue == nil {
		return nil, errors.New(
			"project after-commit queue is unavailable",
		)
	}
	return queue, nil
}

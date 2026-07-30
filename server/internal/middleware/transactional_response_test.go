package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestProjectTransactionResponseDoesNotEmitBeforeCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	buffered, err := NewTransactionalResponseBuffer(context.Writer)
	if err != nil {
		t.Fatal(err)
	}
	defer buffered.Close()

	buffered.Header().Set("X-Project-Result", "committed")
	buffered.WriteHeader(http.StatusCreated)
	payload := bytes.Repeat(
		[]byte("x"),
		transactionalResponseMemoryThreshold+1,
	)
	if _, err := buffered.Write(payload); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf(
			"response escaped before commit: status=%d bytes=%d",
			recorder.Code,
			recorder.Body.Len(),
		)
	}
	if buffered.spill == nil {
		t.Fatal("attachment-sized response did not spill to disk")
	}
	spillName := buffered.spill.Name()

	if err := buffered.Commit(); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusCreated ||
		recorder.Header().Get("X-Project-Result") != "committed" ||
		!bytes.Equal(recorder.Body.Bytes(), payload) {
		t.Fatalf(
			"committed response mismatch: status=%d bytes=%d",
			recorder.Code,
			recorder.Body.Len(),
		)
	}
	if err := buffered.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spillName); !os.IsNotExist(err) {
		t.Fatalf("transactional response spool was not removed: %v", err)
	}
}

func TestProjectTransactionResponseRejectsStreamingSideEffects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	buffered, err := NewTransactionalResponseBuffer(context.Writer)
	if err != nil {
		t.Fatal(err)
	}
	defer buffered.Close()

	if _, _, err := buffered.Hijack(); err == nil {
		t.Fatal("transactional project response allowed connection hijacking")
	}
	if pusher := buffered.Pusher(); pusher != nil {
		t.Fatal("transactional project response exposed HTTP/2 push")
	}
	buffered.Flush()
	if !buffered.Written() || buffered.Size() != 0 {
		t.Fatalf(
			"buffered flush state: written=%v size=%d",
			buffered.Written(),
			buffered.Size(),
		)
	}
}

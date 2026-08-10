package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestProjectAfterCommitResponseQueueIsNarrowAndImmutable(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	newContext := func(t *testing.T) *gin.Context {
		t.Helper()
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		if err := InstallProjectAfterCommitQueue(context); err != nil {
			t.Fatal(err)
		}
		return context
	}
	valid := func() ProjectAfterCommitResponse {
		return ProjectAfterCommitResponse{
			Status:      http.StatusConflict,
			ContentType: "application/problem+json",
			Header:      http.Header{"ETag": {`"v2"`}},
			Body:        []byte(`{"code":"stable_conflict"}`),
		}
	}
	for _, test := range []struct {
		name   string
		change func(*ProjectAfterCommitResponse)
	}{
		{
			name: "ordinary bad request",
			change: func(response *ProjectAfterCommitResponse) {
				response.Status = http.StatusBadRequest
			},
		},
		{
			name: "server error",
			change: func(response *ProjectAfterCommitResponse) {
				response.Status = http.StatusInternalServerError
			},
		},
		{
			name: "non problem body",
			change: func(response *ProjectAfterCommitResponse) {
				response.ContentType = "application/json"
			},
		},
		{
			name: "unsafe header",
			change: func(response *ProjectAfterCommitResponse) {
				response.Header.Set("Set-Cookie", "unsafe")
			},
		},
		{
			name: "oversized body",
			change: func(response *ProjectAfterCommitResponse) {
				response.Body = []byte(
					strings.Repeat(
						"x",
						projectAfterCommitResponseMaximumBytes+1,
					),
				)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := valid()
			test.change(&response)
			if err := QueueProjectAfterCommitResponse(
				newContext(t),
				response,
			); err == nil {
				t.Fatal("unsafe after-commit response was accepted")
			}
		})
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	if err := InstallProjectAfterCommitQueue(context); err != nil {
		t.Fatal(err)
	}
	response := valid()
	if err := QueueProjectAfterCommitResponse(context, response); err != nil {
		t.Fatal(err)
	}
	response.Body[0] = '['
	response.Header.Set("ETag", `"v99"`)
	if err := QueueProjectAfterCommitResponse(
		context,
		valid(),
	); err == nil {
		t.Fatal("duplicate after-commit response was accepted")
	}
	emitted, err := EmitProjectAfterCommitResponse(context)
	if err != nil {
		t.Fatal(err)
	}
	if !emitted ||
		recorder.Code != http.StatusConflict ||
		recorder.Header().Get("ETag") != `"v2"` ||
		recorder.Body.String() != `{"code":"stable_conflict"}` {
		t.Fatalf(
			"queued response mutated: status=%d headers=%v body=%s",
			recorder.Code,
			recorder.Header(),
			recorder.Body.String(),
		)
	}
}

type shortProjectAfterCommitWriter struct {
	gin.ResponseWriter
}

func (writer shortProjectAfterCommitWriter) Write(
	payload []byte,
) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	return len(payload) - 1, nil
}

func TestProjectAfterCommitResponseReportsShortWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Writer = shortProjectAfterCommitWriter{
		ResponseWriter: context.Writer,
	}
	if err := InstallProjectAfterCommitQueue(context); err != nil {
		t.Fatal(err)
	}
	if err := QueueProjectAfterCommitResponse(
		context,
		ProjectAfterCommitResponse{
			Status:      http.StatusConflict,
			ContentType: "application/problem+json",
			Body:        []byte(`{"code":"stable_conflict"}`),
		},
	); err != nil {
		t.Fatal(err)
	}
	emitted, err := EmitProjectAfterCommitResponse(context)
	if !emitted || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf(
			"short response write = emitted:%v error:%v",
			emitted,
			err,
		)
	}
}

package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDecodeStrictJSONRejectsUnpublishedAndTrailingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type request struct {
		Name string `json:"name" binding:"required"`
	}
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "canonical", body: `{"name":"accepted"}`},
		{name: "unknown", body: `{"name":"rejected","ghost":true}`, wantErr: true},
		{name: "trailing", body: `{"name":"rejected"} {}`, wantErr: true},
		{name: "empty", body: ``, wantErr: true},
		{name: "validation", body: `{"name":""}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(
				http.MethodPost,
				"/strict-json",
				bytes.NewBufferString(test.body),
			)
			var value request
			err := decodeStrictJSON(context, &value)
			if (err != nil) != test.wantErr {
				t.Fatalf("decodeStrictJSON() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

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

func TestDecodeStrictJSONObjectPreservesTopLevelFieldPresence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type request struct {
		Name   string  `json:"name" binding:"required"`
		Status *string `json:"status"`
	}
	tests := []struct {
		name       string
		body       string
		wantErr    bool
		wantFields []string
	}{
		{
			name:       "omitted optional field",
			body:       `{"name":"accepted"}`,
			wantFields: []string{"name"},
		},
		{
			name:       "explicit null remains present",
			body:       `{"name":"accepted","status":null}`,
			wantFields: []string{"name", "status"},
		},
		{
			name:    "unknown field",
			body:    `{"name":"rejected","ghost":true}`,
			wantErr: true,
		},
		{
			name:    "case-insensitive Go match is not canonical JSON",
			body:    `{"Name":"rejected"}`,
			wantErr: true,
		},
		{
			name:    "trailing value",
			body:    `{"name":"rejected"} {}`,
			wantErr: true,
		},
		{
			name:    "null is not an object",
			body:    `null`,
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(
				http.MethodPost,
				"/strict-json-object",
				bytes.NewBufferString(test.body),
			)
			var value request
			fields, err := decodeStrictJSONObject(context, &value)
			if (err != nil) != test.wantErr {
				t.Fatalf(
					"decodeStrictJSONObject() error = %v, wantErr %v",
					err,
					test.wantErr,
				)
			}
			if test.wantErr {
				return
			}
			for _, field := range test.wantFields {
				if _, ok := fields[field]; !ok {
					t.Errorf("field %q was not reported present", field)
				}
			}
			if len(fields) != len(test.wantFields) {
				t.Errorf("fields=%v, want %v", fields, test.wantFields)
			}
		})
	}
}

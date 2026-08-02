package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type recordingEmailConfigService struct {
	services.EmailConfigServiceInterface
	calls   int
	request *models.EmailConfigUpdateRequest
}

func (s *recordingEmailConfigService) UpdateEmailConfig(
	_ context.Context,
	request *models.EmailConfigUpdateRequest,
	_ uint,
) (*models.EmailConfig, error) {
	s.calls++
	s.request = request
	config := models.DefaultEmailConfig()
	config.ID = 1
	if request.SMTPHost != nil {
		config.SMTPHost = *request.SMTPHost
	}
	return config, nil
}

func TestUpdateEmailConfigAcceptsHostnameAndIPLiteral(t *testing.T) {
	for _, host := range []string{
		"localhost",
		"smtp.example.com",
		"mail2.example.com",
		"127.0.0.1",
		"192.0.2.25",
		"::1",
		"2001:db8::25",
	} {
		t.Run(host, func(t *testing.T) {
			service := &recordingEmailConfigService{}
			response := updateEmailConfigRequest(
				t,
				service,
				map[string]string{"smtp_host": host},
			)

			if response.Code != http.StatusOK {
				t.Fatalf(
					"SMTP主机 %q 状态码=%d，期望=%d；响应=%s",
					host,
					response.Code,
					http.StatusOK,
					response.Body.String(),
				)
			}
			if service.calls != 1 ||
				service.request == nil ||
				service.request.SMTPHost == nil ||
				*service.request.SMTPHost != host {
				t.Fatalf(
					"SMTP主机 %q 未传递给服务：calls=%d request=%+v",
					host,
					service.calls,
					service.request,
				)
			}
		})
	}
}

func TestUpdateEmailConfigRejectsStructuredOrInjectedSMTPHost(t *testing.T) {
	for _, host := range []string{
		"smtp://localhost",
		"localhost:2525",
		"localhost/path",
		"local host",
		"localhost\t",
		"localhost\r\nRCPT TO:<victim@example.com>",
		"localhost\x00",
		"[::1]",
	} {
		t.Run(host, func(t *testing.T) {
			service := &recordingEmailConfigService{}
			response := updateEmailConfigRequest(
				t,
				service,
				map[string]string{"smtp_host": host},
			)

			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"非法SMTP主机 %q 状态码=%d，期望=%d；响应=%s",
					host,
					response.Code,
					http.StatusBadRequest,
					response.Body.String(),
				)
			}
			if service.calls != 0 {
				t.Fatalf("非法SMTP主机 %q 调用了服务 %d 次", host, service.calls)
			}

			var payload struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
				Data string `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("解析错误响应失败: %v；响应=%s", err, response.Body.String())
			}
			if payload.Code != http.StatusBadRequest ||
				payload.Msg != "invalid_smtp_host" ||
				payload.Data != invalidSMTPHostMessage {
				t.Fatalf("非法SMTP主机响应不稳定：%+v", payload)
			}
		})
	}
}

func TestUpdateEmailConfigRejectsInvalidFromEmailWithSpecificResponse(t *testing.T) {
	service := &recordingEmailConfigService{}
	response := updateEmailConfigRequest(
		t,
		service,
		map[string]string{"from_email": "not-an-email"},
	)

	if response.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf(
			"非法发件人邮箱状态码=%d calls=%d；响应=%s",
			response.Code,
			service.calls,
			response.Body.String(),
		)
	}
	var payload struct {
		Msg  string `json:"msg"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Msg != "invalid_from_email" ||
		payload.Data != "发件人邮箱格式无效" {
		t.Fatalf("非法发件人邮箱响应不稳定：%+v", payload)
	}
}

func updateEmailConfigRequest(
	t *testing.T,
	service services.EmailConfigServiceInterface,
	payload map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)

	handler := NewEmailConfigHandler(service)
	router := gin.New()
	router.PUT("/email-config", func(context *gin.Context) {
		context.Set("user_id", uint(7))
		handler.UpdateEmailConfig(context)
	})

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPut,
		"/email-config",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

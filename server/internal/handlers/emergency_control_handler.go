package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

const emergencyControlRequestLimit = 4 << 10

type emergencyControlUpdateRequest struct {
	GlobalReadOnly *bool `json:"global_read_only"`
	EmergencyStop  *bool `json:"emergency_stop"`
}

type emergencyControlSnapshot struct {
	GlobalReadOnly bool      `json:"global_read_only"`
	EmergencyStop  bool      `json:"emergency_stop"`
	Version        uint64    `json:"version"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type emergencyControlRuntime interface {
	ReadPlatformControls(
		context.Context,
	) (bool, bool, uint64, time.Time, error)
	CompareAndSwapPlatformControls(
		context.Context,
		uint64,
		*bool,
		*bool,
		models.ActorRef,
	) (bool, bool, uint64, time.Time, error)
}

type emergencyControlVersionConflict interface {
	error
	CurrentVersion() uint64
}

// EmergencyControlHandler is the dedicated platform safety adapter. Exact
// emergency_operator authorization is mounted by app.Run; this handler also
// requires the authenticated Human ActorRef before any mutation.
type EmergencyControlHandler struct {
	control emergencyControlRuntime
}

func NewEmergencyControlHandler(
	control emergencyControlRuntime,
) *EmergencyControlHandler {
	return &EmergencyControlHandler{control: control}
}

func (h *EmergencyControlHandler) Get(c *gin.Context) {
	if h == nil || h.control == nil {
		writeEmergencyControlError(
			c,
			http.StatusServiceUnavailable,
			"service_unavailable",
			"安全与应急控制服务不可用",
		)
		return
	}
	globalReadOnly, emergencyStop, version, updatedAt, err :=
		h.control.ReadPlatformControls(c.Request.Context())
	if err != nil {
		writeEmergencyControlError(
			c,
			http.StatusServiceUnavailable,
			"service_unavailable",
			"安全与应急控制状态暂时不可用",
		)
		return
	}
	snapshot := emergencyControlSnapshot{
		GlobalReadOnly: globalReadOnly,
		EmergencyStop:  emergencyStop,
		Version:        version,
		UpdatedAt:      updatedAt,
	}
	setEmergencyControlHeaders(c, version)
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "安全与应急控制状态获取成功",
		"data": snapshot,
	})
}

func (h *EmergencyControlHandler) Update(c *gin.Context) {
	if h == nil || h.control == nil {
		writeEmergencyControlError(
			c,
			http.StatusServiceUnavailable,
			"service_unavailable",
			"安全与应急控制服务不可用",
		)
		return
	}
	expectedVersion, err := httpcontract.ParseIfMatch(
		strings.TrimSpace(c.GetHeader("If-Match")),
	)
	switch {
	case errors.Is(err, httpcontract.ErrIfMatchRequired):
		writeEmergencyControlError(
			c,
			http.StatusPreconditionRequired,
			"precondition_required",
			"必须提供当前安全控制版本对应的 If-Match 请求头",
		)
		return
	case err != nil:
		writeEmergencyControlError(
			c,
			http.StatusBadRequest,
			"invalid_request",
			`If-Match 必须使用强 ETag 格式，例如 "v1"`,
		)
		return
	}

	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		emergencyControlRequestLimit,
	)
	var request emergencyControlUpdateRequest
	if err := decodeStrictJSON(c, &request); err != nil {
		writeEmergencyControlError(
			c,
			http.StatusBadRequest,
			"invalid_request",
			"请求内容无效，只允许提交明确的布尔控制字段",
		)
		return
	}
	if request.GlobalReadOnly == nil && request.EmergencyStop == nil {
		writeEmergencyControlError(
			c,
			http.StatusBadRequest,
			"invalid_request",
			"至少需要修改一个安全控制",
		)
		return
	}
	userID, ok := middleware.GetCurrentUserID(c)
	if !ok || userID == 0 {
		writeEmergencyControlError(
			c,
			http.StatusForbidden,
			"invalid_actor",
			"无法确认当前应急操作人员身份",
		)
		return
	}
	globalReadOnly, emergencyStop, version, updatedAt, err :=
		h.control.CompareAndSwapPlatformControls(
			c.Request.Context(),
			expectedVersion,
			request.GlobalReadOnly,
			request.EmergencyStop,
			models.HumanActor(userID),
		)
	if err != nil {
		var conflict emergencyControlVersionConflict
		switch {
		case errors.As(err, &conflict):
			setEmergencyControlHeaders(c, conflict.CurrentVersion())
			writeEmergencyControlError(
				c,
				http.StatusPreconditionFailed,
				"version_conflict",
				"安全控制已被其他操作更新，请刷新后重试",
			)
		default:
			writeEmergencyControlError(
				c,
				http.StatusServiceUnavailable,
				"service_unavailable",
				"安全控制未能持久化，Agent 写入已按失败关闭策略停止",
			)
		}
		return
	}
	snapshot := emergencyControlSnapshot{
		GlobalReadOnly: globalReadOnly,
		EmergencyStop:  emergencyStop,
		Version:        version,
		UpdatedAt:      updatedAt,
	}
	setEmergencyControlHeaders(c, version)
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "安全与应急控制已更新",
		"data": snapshot,
	})
}

func setEmergencyControlHeaders(c *gin.Context, version uint64) {
	c.Header("Cache-Control", "no-store")
	c.Header("ETag", httpcontract.FormatETag(version))
}

func writeEmergencyControlError(
	c *gin.Context,
	status int,
	code string,
	message string,
) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{
		"code": code,
		"msg":  message,
	})
}

package a2a

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const maxRPCBodyBytes = 2 << 20

type ServerOptions struct {
	Card           *AgentCard
	ExtendedCard   *AgentCard
	CardOptions    CardOptions
	ServiceOptions ServiceOptions
	Heartbeat      time.Duration
}

type Server struct {
	service      *Service
	card         AgentCard
	extendedCard *AgentCard
	cardDoc      cardDocument
	heartbeat    time.Duration
}

func NewServer(store Store, backend Backend, opts ServerOptions) (*Server, error) {
	card := DefaultAgentCard(opts.CardOptions)
	if opts.Card != nil {
		card = *opts.Card
	}
	if opts.ExtendedCard != nil {
		card.Capabilities.ExtendedAgentCard = true
	}
	cardDoc, err := newCardDocument(card)
	if err != nil {
		return nil, err
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = 15 * time.Second
	}
	opts.ServiceOptions.acceptedInputModes = append(
		[]string(nil),
		card.DefaultInputModes...,
	)
	opts.ServiceOptions.acceptedOutputModes = append(
		[]string(nil),
		card.DefaultOutputModes...,
	)
	return &Server{
		service:      NewService(store, backend, opts.ServiceOptions),
		card:         card,
		extendedCard: cloneAgentCard(opts.ExtendedCard),
		cardDoc:      cardDoc,
		heartbeat:    opts.Heartbeat,
	}, nil
}

func (s *Server) Service() *Service {
	return s.service
}

func (s *Server) AgentCard() AgentCard {
	return s.card
}

// CardHandler exposes the public discovery document independently so callers
// can keep it outside authentication middleware.
func (s *Server) CardHandler() gin.HandlerFunc {
	return s.cardDoc.Handler
}

// RPCHandler exposes the protocol endpoint independently so callers can apply
// OAuth and scope middleware only to A2A operations.
func (s *Server) RPCHandler() gin.HandlerFunc {
	return s.HandleJSONRPC
}

// RegisterRoutes mounts both A2A endpoints on a Gin engine or group.
// Authentication/scope middleware should be applied by the caller to /a2a/v1;
// the well-known Agent Card is intentionally public.
func (s *Server) RegisterRoutes(routes gin.IRoutes) {
	routes.GET(AgentCardPath, s.CardHandler())
	routes.POST(RPCPath, s.RPCHandler())
}

func (s *Server) HandleJSONRPC(c *gin.Context) {
	if err := validateContentType(c.GetHeader("Content-Type")); err != nil {
		c.JSON(http.StatusUnsupportedMediaType, JSONRPCResponse{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage("null"),
			Error: rpcError(
				-32005,
				"The request Content-Type is not supported",
				"CONTENT_TYPE_NOT_SUPPORTED",
				map[string]string{"supportedContentType": "application/json"},
			),
		})
		return
	}
	version := requestedA2AVersion(c)
	if version != ProtocolVersion {
		// A2A 1.0 requires an empty service parameter to be interpreted as
		// protocol 0.3. ChronoDesk exposes only the 1.0 interface, so both a
		// missing header and every other version are rejected deterministically.
		requestedVersion := version
		if requestedVersion == "" {
			requestedVersion = "0.3"
		}
		s.writeError(c, nil, -32009, "A2A protocol version is not supported", "VERSION_NOT_SUPPORTED", map[string]string{
			"requestedVersion": requestedVersion,
			"supportedVersion": ProtocolVersion,
		})
		return
	}
	request, parseError := decodeRPCRequest(c)
	if parseError != nil {
		c.JSON(http.StatusBadRequest, JSONRPCResponse{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage("null"),
			Error:   rpcError(-32700, "Invalid JSON payload", "JSON_PARSE_ERROR", nil),
		})
		return
	}
	if err := request.Validate(); err != nil {
		s.writeError(c, request.ID, -32600, "Request payload validation error", "INVALID_REQUEST", nil)
		return
	}
	switch CanonicalMethod(request.Method) {
	case "SendStreamingMessage":
		s.handleSendStream(c, request)
	case "SubscribeToTask":
		s.handleSubscribe(c, request)
	default:
		result, err := s.dispatch(c, request)
		if err != nil {
			s.writeServiceError(c, request.ID, err)
			return
		}
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: JSONRPCVersion,
			ID:      request.ID,
			Result:  result,
		})
	}
}

func (s *Server) dispatch(c *gin.Context, request JSONRPCRequest) (any, error) {
	ctx := c.Request.Context()
	switch CanonicalMethod(request.Method) {
	case "SendMessage":
		var params SendMessageParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		task, err := s.service.SendMessage(ctx, params)
		if err != nil {
			return nil, err
		}
		return SendMessageResult{Task: &task}, nil
	case "GetTask":
		var params GetTaskParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.service.GetTask(ctx, params)
	case "ListTasks":
		var params ListTasksParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.service.ListTasks(ctx, params)
	case "CancelTask":
		var params TaskIDParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if params.ID == "" {
			return nil, errors.New("id is required")
		}
		return s.service.CancelTask(ctx, params.ID)
	case "CreateTaskPushNotificationConfig":
		var params PushNotificationConfig
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.service.CreatePushConfig(ctx, params)
	case "GetTaskPushNotificationConfig":
		var params GetPushConfigParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.service.GetPushConfig(ctx, params)
	case "ListTaskPushNotificationConfigs":
		var params ListPushConfigsParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.service.ListPushConfigs(ctx, params)
	case "DeleteTaskPushNotificationConfig":
		var params GetPushConfigParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if err := s.service.DeletePushConfig(ctx, params); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	case "GetExtendedAgentCard":
		var params GetExtendedAgentCardParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if !s.card.Capabilities.ExtendedAgentCard {
			return nil, ErrUnsupported
		}
		if s.extendedCard == nil {
			return nil, ErrExtendedAgentCardNotConfigured
		}
		return *cloneAgentCard(s.extendedCard), nil
	default:
		return nil, methodNotFoundError{method: request.Method}
	}
}

func (s *Server) handleSendStream(c *gin.Context, request JSONRPCRequest) {
	var params SendMessageParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.writeServiceError(c, request.ID, err)
		return
	}
	task, replayed, err := s.service.StartMessageOnce(c.Request.Context(), params)
	if err != nil {
		s.writeServiceError(c, request.ID, err)
		return
	}
	if replayed &&
		(task.Status.State.IsTerminal() || task.Status.State.IsInterrupted()) {
		closed := make(chan StoredEvent)
		close(closed)
		s.streamEvents(c, request.ID, task, nil, closed, true)
		return
	}
	live, unsubscribe, err := s.service.Subscribe(c.Request.Context(), task.ID)
	if err != nil {
		s.writeServiceError(c, request.ID, err)
		return
	}
	defer unsubscribe()
	replay, err := s.service.Replay(c.Request.Context(), task.ID, c.GetHeader("Last-Event-ID"))
	if err != nil {
		s.writeServiceError(c, request.ID, err)
		return
	}
	if !replayed || taskNeedsRecovery(task) {
		s.service.ExecuteAsync(c.Request.Context(), task, params.Message)
	}
	s.streamEvents(c, request.ID, task, replay, live, false)
}

func (s *Server) handleSubscribe(c *gin.Context, request JSONRPCRequest) {
	var params TaskIDParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.writeServiceError(c, request.ID, err)
		return
	}
	if params.ID == "" {
		s.writeServiceError(c, request.ID, errors.New("id is required"))
		return
	}
	task, err := s.service.GetTask(c.Request.Context(), GetTaskParams{ID: params.ID})
	if err != nil {
		s.writeServiceError(c, request.ID, err)
		return
	}
	live, unsubscribe, err := s.service.Subscribe(c.Request.Context(), task.ID)
	if err != nil {
		s.writeServiceError(c, request.ID, err)
		return
	}
	defer unsubscribe()
	replay, err := s.service.Replay(c.Request.Context(), task.ID, c.GetHeader("Last-Event-ID"))
	if err != nil {
		s.writeServiceError(c, request.ID, err)
		return
	}
	s.streamEvents(c, request.ID, task, replay, live, true)
}

func (s *Server) streamEvents(
	c *gin.Context,
	requestID json.RawMessage,
	task Task,
	replay []StoredEvent,
	live <-chan StoredEvent,
	includeSnapshot bool,
) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	c.Writer.Flush()

	seen := make(map[string]struct{}, len(replay))
	if includeSnapshot {
		if err := s.service.authorizeTaskSnapshot(c.Request.Context(), task); err != nil {
			return
		}
		if err := writeSSE(c.Writer, "", JSONRPCResponse{
			JSONRPC: JSONRPCVersion,
			ID:      requestID,
			Result:  StreamResponse{Task: taskPointer(task)},
		}); err != nil {
			return
		}
		c.Writer.Flush()
	}
	for index, event := range replay {
		if _, duplicate := seen[event.Cursor]; duplicate {
			continue
		}
		seen[event.Cursor] = struct{}{}
		if err := s.service.authorizeTaskSnapshot(
			c.Request.Context(),
			taskWithStreamSnapshot(task, event.Payload),
		); err != nil {
			return
		}
		if err := writeSSE(c.Writer, event.Cursor, JSONRPCResponse{
			JSONRPC: JSONRPCVersion,
			ID:      requestID,
			Result:  event.Payload,
		}); err != nil {
			return
		}
		c.Writer.Flush()
		if index == len(replay)-1 &&
			(event.Payload.Terminal() || event.Payload.Interrupted()) {
			return
		}
	}
	if len(replay) == 0 && includeSnapshot && (task.Status.State.IsTerminal() || task.Status.State.IsInterrupted()) {
		return
	}

	heartbeat := time.NewTicker(s.heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case event, ok := <-live:
			if !ok {
				return
			}
			if _, duplicate := seen[event.Cursor]; duplicate {
				continue
			}
			seen[event.Cursor] = struct{}{}
			if err := s.service.authorizeTaskSnapshot(
				c.Request.Context(),
				taskWithStreamSnapshot(task, event.Payload),
			); err != nil {
				return
			}
			if err := writeSSE(c.Writer, event.Cursor, JSONRPCResponse{
				JSONRPC: JSONRPCVersion,
				ID:      requestID,
				Result:  event.Payload,
			}); err != nil {
				return
			}
			c.Writer.Flush()
			if event.Payload.Terminal() || event.Payload.Interrupted() {
				return
			}
		case <-heartbeat.C:
			if _, err := io.WriteString(c.Writer, ": keep-alive\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

func writeSSE(writer io.Writer, eventID string, response JSONRPCResponse) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	buffer := bufio.NewWriter(writer)
	if eventID != "" {
		if _, err := buffer.WriteString("id: " + eventID + "\n"); err != nil {
			return err
		}
	}
	if _, err := buffer.WriteString("data: "); err != nil {
		return err
	}
	if _, err := buffer.Write(payload); err != nil {
		return err
	}
	if _, err := buffer.WriteString("\n\n"); err != nil {
		return err
	}
	return buffer.Flush()
}

func (s *Server) writeServiceError(c *gin.Context, id json.RawMessage, err error) {
	switch {
	case isMethodNotFound(err):
		s.writeError(c, id, -32601, "Method not found", "METHOD_NOT_FOUND", nil)
	case errors.Is(err, ErrTaskNotFound), errors.Is(err, ErrPushConfigNotFound):
		s.writeError(c, id, -32001, "The specified task or resource does not exist or is not accessible", "TASK_NOT_FOUND", nil)
	case errors.Is(err, ErrTaskNotCancelable):
		s.writeError(c, id, -32002, "The task is not in a cancelable state", "TASK_NOT_CANCELABLE", nil)
	case errors.Is(err, ErrPushUnavailable):
		s.writeError(c, id, -32003, "Push notifications are not supported", "PUSH_NOTIFICATION_NOT_SUPPORTED", nil)
	case errors.Is(err, ErrContentTypeNotSupported):
		var unsupported *contentTypeNotSupportedError
		metadata := map[string]string(nil)
		if errors.As(err, &unsupported) &&
			strings.TrimSpace(unsupported.mediaType) != "" {
			metadata = map[string]string{
				"mediaType": unsupported.mediaType,
			}
		}
		s.writeError(c, id, -32005, "The requested content type is not supported", "CONTENT_TYPE_NOT_SUPPORTED", metadata)
	case errors.Is(err, ErrUnsupported), errors.Is(err, ErrTaskBusy), errors.Is(err, ErrInvalidTransition):
		s.writeError(c, id, -32004, "The operation is not supported for the current task state", "UNSUPPORTED_OPERATION", nil)
	case errors.Is(err, ErrExtendedAgentCardNotConfigured):
		s.writeError(c, id, -32007, "The extended Agent Card is not configured", "EXTENDED_AGENT_CARD_NOT_CONFIGURED", nil)
	case errors.Is(err, ErrInvalidPageToken), errors.Is(err, ErrInvalidEventCursor):
		s.writeError(c, id, -32602, "Invalid parameters", "INVALID_PARAMS", nil)
	default:
		var paramsError *paramsDecodeError
		if errors.As(err, &paramsError) || isClientInputError(err) {
			s.writeError(c, id, -32602, "Invalid parameters", "INVALID_PARAMS", map[string]string{
				"detail": err.Error(),
			})
			return
		}
		s.writeError(c, id, -32603, "Internal error", "INTERNAL_ERROR", nil)
	}
}

func requestedA2AVersion(c *gin.Context) string {
	headerVersion := strings.TrimSpace(c.GetHeader("A2A-Version"))
	queryVersion := strings.TrimSpace(c.Query("A2A-Version"))
	if headerVersion != "" && queryVersion != "" && headerVersion != queryVersion {
		return headerVersion + "," + queryVersion
	}
	if headerVersion != "" {
		return headerVersion
	}
	return queryVersion
}

func cloneAgentCard(card *AgentCard) *AgentCard {
	if card == nil {
		return nil
	}
	var cloned AgentCard
	data, err := json.Marshal(card)
	if err != nil {
		return nil
	}
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil
	}
	return &cloned
}

func (s *Server) writeError(c *gin.Context, id json.RawMessage, code int, message, reason string, metadata map[string]string) {
	if len(bytes.TrimSpace(id)) == 0 {
		id = json.RawMessage("null")
	}
	c.JSON(http.StatusOK, JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error:   rpcError(code, message, reason, metadata),
	})
}

func rpcError(code int, message, reason string, metadata map[string]string) *JSONRPCError {
	return &JSONRPCError{
		Code:    code,
		Message: message,
		Data:    errorDetail(reason, metadata),
	}
}

func validateContentType(value string) error {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return err
	}
	switch strings.ToLower(mediaType) {
	case "application/json":
		return nil
	default:
		return errors.New("unsupported content type")
	}
}

func decodeRPCRequest(c *gin.Context) (JSONRPCRequest, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRPCBodyBytes)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return JSONRPCRequest{}, err
	}
	return DecodeJSONRPCRequest(raw)
}

type paramsDecodeError struct {
	err error
}

func (e *paramsDecodeError) Error() string {
	return e.err.Error()
}

func (e *paramsDecodeError) Unwrap() error {
	return e.err
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = json.RawMessage("{}")
	}
	if err := decodeExactJSON(raw, target); err != nil {
		return &paramsDecodeError{err: err}
	}
	return nil
}

type methodNotFoundError struct {
	method string
}

func (e methodNotFoundError) Error() string {
	return "method not found: " + e.method
}

func isMethodNotFound(err error) bool {
	var target methodNotFoundError
	return errors.As(err, &target)
}

func isClientInputError(err error) bool {
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"required", "invalid", "must ", "cannot ", "does not match",
		"already exists", "historylength", "linkedticketid",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

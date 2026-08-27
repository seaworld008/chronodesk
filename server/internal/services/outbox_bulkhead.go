package services

import (
	"sync"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	maxOutboxDeliveryConcurrency = 8
	outboxClaimClassCount        = 11
)

// OutboxDeliveryLane is a fixed, low-cardinality bulkhead label. Destination
// identifiers and other untrusted values never become lane or metric labels.
type OutboxDeliveryLane string

const (
	OutboxDeliveryLaneWebhook  OutboxDeliveryLane = "webhook"
	OutboxDeliveryLaneCallback OutboxDeliveryLane = "callback"
	OutboxDeliveryLaneStorage  OutboxDeliveryLane = "storage"
	OutboxDeliveryLaneInternal OutboxDeliveryLane = "internal"
	OutboxDeliveryLaneOther    OutboxDeliveryLane = "other"
)

var outboxDeliveryLaneOrder = [...]OutboxDeliveryLane{
	OutboxDeliveryLaneWebhook,
	OutboxDeliveryLaneCallback,
	OutboxDeliveryLaneStorage,
	OutboxDeliveryLaneInternal,
	OutboxDeliveryLaneOther,
}

var outboxNonWebhookDeliveryLaneOrder = [...]OutboxDeliveryLane{
	OutboxDeliveryLaneCallback,
	OutboxDeliveryLaneStorage,
	OutboxDeliveryLaneInternal,
	OutboxDeliveryLaneOther,
}

var (
	outboxCallbackDestinations = []string{
		"a2a_push",
		EmailOutboxDestination,
	}
	outboxStorageDestinations = []string{
		AttachmentUploadOutboxDestination,
		AttachmentCleanupOutboxDestination,
		AttachmentStagingCleanupOutboxDestination,
		KnowledgeIndexRebuildOutboxDestination,
	}
	outboxInternalDestinations = []string{
		"event_stream",
		"automation",
		NotificationOutboxDestination,
		"sla",
		SLAEscalationOutboxDestination,
	}
	outboxKnownDestinations = []string{
		"webhook",
		"a2a_push",
		EmailOutboxDestination,
		AttachmentUploadOutboxDestination,
		AttachmentCleanupOutboxDestination,
		AttachmentStagingCleanupOutboxDestination,
		KnowledgeIndexRebuildOutboxDestination,
		"event_stream",
		"automation",
		NotificationOutboxDestination,
		"sla",
		SLAEscalationOutboxDestination,
	}
)

// OutboxBatchStatus distinguishes an empty queue from capacity contention.
// Partial saturation means at least one healthy lane still made progress.
type OutboxBatchStatus string

const (
	OutboxBatchStatusIdle              OutboxBatchStatus = "idle"
	OutboxBatchStatusProcessed         OutboxBatchStatus = "processed"
	OutboxBatchStatusSaturated         OutboxBatchStatus = "saturated"
	OutboxBatchStatusPartialSaturation OutboxBatchStatus = "partial_saturation"
)

type outboxDeliveryPermit struct {
	service *AgentNativeService
	lane    OutboxDeliveryLane
	once    sync.Once
}

type claimedOutboxDelivery struct {
	delivery *models.OutboxDelivery
	permit   *outboxDeliveryPermit
}

type outboxClaimResult struct {
	deliveries      []claimedOutboxDelivery
	backstoppedDead int
	scanned         int
	globalSaturated bool
	saturatedLanes  []OutboxDeliveryLane
}

type outboxPermitBlock uint8

const (
	outboxPermitAvailable outboxPermitBlock = iota
	outboxPermitLaneSaturated
	outboxPermitGlobalSaturated
)

func normalizeOutboxDeliveryConcurrency(value int) int {
	if value <= 0 {
		return maxOutboxDeliveryConcurrency
	}
	if value > maxOutboxDeliveryConcurrency {
		return maxOutboxDeliveryConcurrency
	}
	return value
}

// configureOutboxDeliveryBulkheads is used only during service construction
// (and by isolated tests before workers start). Replacing live channels would
// orphan permits and is therefore deliberately not exposed as runtime config.
func (s *AgentNativeService) configureOutboxDeliveryBulkheads(
	globalLimit int,
) {
	globalLimit = normalizeOutboxDeliveryConcurrency(globalLimit)
	laneLimit := globalLimit / 2
	if laneLimit < 1 {
		laneLimit = 1
	}
	s.outboxDeliverySlots = make(chan struct{}, globalLimit)
	s.outboxDeliveryLaneSlots = make(
		map[OutboxDeliveryLane]chan struct{},
		len(outboxDeliveryLaneOrder),
	)
	for _, lane := range outboxDeliveryLaneOrder {
		s.outboxDeliveryLaneSlots[lane] = make(chan struct{}, laneLimit)
	}
}

func (s *AgentNativeService) tryAcquireOutboxDeliveryPermit(
	lane OutboxDeliveryLane,
) (*outboxDeliveryPermit, outboxPermitBlock) {
	if s == nil || s.outboxDeliverySlots == nil {
		return nil, outboxPermitGlobalSaturated
	}
	laneSlots := s.outboxDeliveryLaneSlots[lane]
	if laneSlots == nil {
		lane = OutboxDeliveryLaneOther
		laneSlots = s.outboxDeliveryLaneSlots[lane]
	}
	if laneSlots == nil {
		return nil, outboxPermitLaneSaturated
	}
	select {
	case laneSlots <- struct{}{}:
	default:
		return nil, outboxPermitLaneSaturated
	}
	select {
	case s.outboxDeliverySlots <- struct{}{}:
		return &outboxDeliveryPermit{
			service: s,
			lane:    lane,
		}, outboxPermitAvailable
	default:
		<-laneSlots
		return nil, outboxPermitGlobalSaturated
	}
}

func (permit *outboxDeliveryPermit) release() {
	if permit == nil || permit.service == nil {
		return
	}
	permit.once.Do(func() {
		<-permit.service.outboxDeliverySlots
		<-permit.service.outboxDeliveryLaneSlots[permit.lane]
	})
}

func outboxDeliveryLaneForDestination(
	destinationType string,
) OutboxDeliveryLane {
	// Webhook receives a dedicated lane because it is the highest-risk
	// non-cooperative callback. Other outbound callbacks, storage/search I/O,
	// and in-process/database consumers each share one bounded lane. Every
	// future or malformed type is contained by the finite fallback lane.
	switch destinationType {
	case "webhook":
		return OutboxDeliveryLaneWebhook
	case "a2a_push", EmailOutboxDestination:
		return OutboxDeliveryLaneCallback
	case AttachmentUploadOutboxDestination,
		AttachmentCleanupOutboxDestination,
		AttachmentStagingCleanupOutboxDestination,
		KnowledgeIndexRebuildOutboxDestination:
		return OutboxDeliveryLaneStorage
	case "event_stream",
		"automation",
		NotificationOutboxDestination,
		"sla",
		SLAEscalationOutboxDestination:
		return OutboxDeliveryLaneInternal
	default:
		return OutboxDeliveryLaneOther
	}
}

func applyOutboxClaimLane(
	query *gorm.DB,
	lane OutboxDeliveryLane,
) *gorm.DB {
	switch lane {
	case OutboxDeliveryLaneCallback:
		return query.Where(
			"destination_type IN ?",
			outboxCallbackDestinations,
		)
	case OutboxDeliveryLaneStorage:
		return query.Where(
			"destination_type IN ?",
			outboxStorageDestinations,
		)
	case OutboxDeliveryLaneInternal:
		return query.Where(
			"destination_type IN ?",
			outboxInternalDestinations,
		)
	case OutboxDeliveryLaneOther:
		return query.Where(
			"destination_type NOT IN ?",
			outboxKnownDestinations,
		)
	default:
		return query.Where("1 = 0")
	}
}

func (result *outboxClaimResult) noteBlockedPermit(
	lane OutboxDeliveryLane,
	blocked outboxPermitBlock,
) {
	if result == nil {
		return
	}
	switch blocked {
	case outboxPermitGlobalSaturated:
		result.globalSaturated = true
	case outboxPermitLaneSaturated:
		for _, current := range result.saturatedLanes {
			if current == lane {
				return
			}
		}
		result.saturatedLanes = append(result.saturatedLanes, lane)
	}
}

func (result *outboxClaimResult) merge(next outboxClaimResult) {
	if result == nil {
		return
	}
	result.deliveries = append(result.deliveries, next.deliveries...)
	result.backstoppedDead += next.backstoppedDead
	result.scanned += next.scanned
	if next.globalSaturated {
		result.globalSaturated = true
	}
	for _, lane := range next.saturatedLanes {
		result.noteBlockedPermit(lane, outboxPermitLaneSaturated)
	}
}

func (result *OutboxBatchResult) noteOutboxSaturation(
	lane OutboxDeliveryLane,
	global bool,
) {
	if result == nil {
		return
	}
	if global {
		result.GlobalSaturated = true
	}
	for _, current := range result.SaturatedLanes {
		if current == lane {
			return
		}
	}
	result.SaturatedLanes = append(result.SaturatedLanes, lane)
}

func (result *OutboxBatchResult) mergeOutboxCapacity(
	wave OutboxBatchResult,
) {
	if result == nil {
		return
	}
	if wave.GlobalSaturated {
		result.GlobalSaturated = true
	}
	for _, lane := range wave.SaturatedLanes {
		result.noteOutboxSaturation(lane, false)
	}
}

func (result *OutboxBatchResult) finalizeOutboxBatchStatus() {
	if result == nil {
		return
	}
	if len(result.SaturatedLanes) > 1 {
		seen := make(map[OutboxDeliveryLane]struct{}, len(result.SaturatedLanes))
		for _, lane := range result.SaturatedLanes {
			seen[lane] = struct{}{}
		}
		result.SaturatedLanes = result.SaturatedLanes[:0]
		for _, lane := range outboxDeliveryLaneOrder {
			if _, ok := seen[lane]; ok {
				result.SaturatedLanes = append(
					result.SaturatedLanes,
					lane,
				)
			}
		}
	}
	saturated := result.GlobalSaturated ||
		len(result.SaturatedLanes) > 0
	progressed := result.Claimed > 0 ||
		result.Delivered > 0 ||
		result.Failed > 0 ||
		result.Dead > 0 ||
		result.Expired > 0
	switch {
	case saturated && progressed:
		result.Status = OutboxBatchStatusPartialSaturation
	case saturated:
		result.Status = OutboxBatchStatusSaturated
	case progressed:
		result.Status = OutboxBatchStatusProcessed
	default:
		result.Status = OutboxBatchStatusIdle
	}
}

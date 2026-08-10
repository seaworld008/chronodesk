package services

import (
	"context"
	"log"
	"sync"
	"time"
)

const (
	webhookAttemptAuditBatchLimit         = 200
	webhookAttemptAuditPersistenceTimeout = 5 * time.Second
	webhookAttemptAuditShutdownTimeout    = 500 * time.Millisecond
	webhookAttemptAuditWaitTimeout        = 10 * time.Second
)

type outboxAttemptAuditBatchContextKey struct{}

type webhookAttemptAuditWork struct {
	source  context.Context
	persist func(context.Context)
}

type outboxAttemptAuditBatch struct {
	mu        sync.Mutex
	capacity  int
	finalized chan struct{}
	items     []webhookAttemptAuditWork
	closed    bool
	startOnce sync.Once
}

func newOutboxAttemptAuditBatch(limit int) *outboxAttemptAuditBatch {
	if limit <= 0 {
		limit = 50
	}
	if limit > webhookAttemptAuditBatchLimit {
		limit = webhookAttemptAuditBatchLimit
	}
	return &outboxAttemptAuditBatch{
		capacity:  limit,
		finalized: make(chan struct{}),
		items:     make([]webhookAttemptAuditWork, 0, limit),
	}
}

func outboxAttemptAuditBatchFromContext(
	ctx context.Context,
) *outboxAttemptAuditBatch {
	if ctx == nil {
		return nil
	}
	batch, _ := ctx.Value(
		outboxAttemptAuditBatchContextKey{},
	).(*outboxAttemptAuditBatch)
	return batch
}

func (batch *outboxAttemptAuditBatch) enqueue(
	work webhookAttemptAuditWork,
) bool {
	if batch == nil || work.persist == nil {
		return false
	}
	batch.mu.Lock()
	defer batch.mu.Unlock()
	if batch.closed || len(batch.items) >= batch.capacity {
		return false
	}
	batch.items = append(batch.items, work)
	return true
}

func (batch *outboxAttemptAuditBatch) finalize() {
	if batch == nil {
		return
	}
	batch.mu.Lock()
	if !batch.closed {
		batch.closed = true
		close(batch.finalized)
	}
	batch.mu.Unlock()
}

func (batch *outboxAttemptAuditBatch) snapshot() []webhookAttemptAuditWork {
	if batch == nil {
		return nil
	}
	batch.mu.Lock()
	defer batch.mu.Unlock()
	return append([]webhookAttemptAuditWork(nil), batch.items...)
}

func (ns *NotificationService) submitWebhookAttemptAudit(
	work webhookAttemptAuditWork,
) {
	if ns == nil || work.persist == nil {
		return
	}
	batch := outboxAttemptAuditBatchFromContext(work.source)
	if batch != nil && batch.enqueue(work) {
		batch.startOnce.Do(func() {
			if ns.startWebhookAttemptAudit() {
				go ns.drainWebhookAttemptAuditBatch(batch)
			}
		})
		return
	}
	// A late adapter may finish after its bounded delivery handoff and after
	// ProcessOutboxBatch has already finalized the claim. It can no longer join
	// the closed batch, but the finalization fence is already behind it, so run
	// the audit as an independently tracked bounded item instead of dropping it.
	if ns.startWebhookAttemptAudit() {
		go func() {
			defer ns.webhookAttemptAudits.Done()
			ns.persistWebhookAttemptAudit(work)
		}()
	}
}

func (ns *NotificationService) startWebhookAttemptAudit() bool {
	ns.webhookAttemptAuditMu.Lock()
	defer ns.webhookAttemptAuditMu.Unlock()
	if ns.webhookAttemptAuditClosed {
		ns.webhookAttemptAuditDrops.Add(1)
		log.Print("webhook post-response audit admission is closed")
		return false
	}
	ns.webhookAttemptAudits.Add(1)
	return true
}

func (ns *NotificationService) drainWebhookAttemptAuditBatch(
	batch *outboxAttemptAuditBatch,
) {
	defer ns.webhookAttemptAudits.Done()
	select {
	case <-batch.finalized:
	case <-ns.webhookAttemptAuditRoot.Done():
		return
	}
	for _, work := range batch.snapshot() {
		if ns.webhookAttemptAuditRoot.Err() != nil {
			return
		}
		ns.persistWebhookAttemptAudit(work)
	}
}

func (ns *NotificationService) persistWebhookAttemptAudit(
	work webhookAttemptAuditWork,
) {
	auditContext, cancel := ns.webhookAttemptAuditContext(work.source)
	defer cancel()
	select {
	case ns.webhookAttemptAuditWriters <- struct{}{}:
		defer func() { <-ns.webhookAttemptAuditWriters }()
	case <-auditContext.Done():
		return
	}
	work.persist(auditContext)
}

func (ns *NotificationService) webhookAttemptAuditContext(
	source context.Context,
) (context.Context, context.CancelFunc) {
	if source == nil {
		source = context.Background()
	}
	auditContext, cancel := context.WithTimeout(
		context.WithoutCancel(source),
		webhookAttemptAuditPersistenceTimeout,
	)
	stopRootCancel := context.AfterFunc(
		ns.webhookAttemptAuditRoot,
		cancel,
	)
	return auditContext, func() {
		stopRootCancel()
		cancel()
	}
}

func (ns *NotificationService) waitForWebhookAttemptAuditsWithin(
	timeout time.Duration,
) bool {
	if ns == nil {
		return true
	}
	if timeout <= 0 {
		timeout = webhookAttemptAuditWaitTimeout
	}
	done := make(chan struct{})
	go func() {
		ns.webhookAttemptAudits.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

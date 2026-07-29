package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

type recordingAgentStreamGuard struct {
	inner *InMemoryAgentExecutionGuard

	mu       sync.Mutex
	requests []AgentExecutionGuardRequest
	releases int
	failAt   int
}

func newRecordingAgentStreamGuard() *recordingAgentStreamGuard {
	return &recordingAgentStreamGuard{
		inner: NewInMemoryAgentExecutionGuardForTesting(),
	}
}

func (g *recordingAgentStreamGuard) Acquire(
	ctx context.Context,
	request AgentExecutionGuardRequest,
) (*AgentExecutionPermit, error) {
	g.mu.Lock()
	g.requests = append(g.requests, request)
	call := len(g.requests)
	g.mu.Unlock()
	if g.failAt > 0 && call == g.failAt {
		return nil, ErrConcurrencyLimit
	}
	return g.inner.Acquire(ctx, request)
}

func (g *recordingAgentStreamGuard) Release(
	ctx context.Context,
	permit *AgentExecutionPermit,
) error {
	g.mu.Lock()
	g.releases++
	g.mu.Unlock()
	return g.inner.Release(ctx, permit)
}

func (g *recordingAgentStreamGuard) RecordLoop(
	ctx context.Context,
	request AgentLoopGuardRequest,
) (bool, error) {
	return g.inner.RecordLoop(ctx, request)
}

func (*recordingAgentStreamGuard) IsDistributed() bool {
	return false
}

func TestAcquireAgentStreamUsesThreeConcurrencyOnlyDimensionsAndIdempotentRelease(t *testing.T) {
	db := openAgentNativeTestDB(t)
	guard := newRecordingAgentStreamGuard()
	service := NewAgentNativeService(db, AgentNativeOptions{
		CredentialPepper: []byte("stream-guard-test-pepper"),
		ExecutionGuard:   guard,
	})
	principal, err := service.CreateServicePrincipal(context.Background(), CreateServicePrincipalInput{
		Name:               "stream-agent",
		Scopes:             []string{models.ScopeTasksManage},
		RateLimitPerMinute: 100,
		ConcurrentLimit:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueCredential(
		context.Background(),
		principal.ID,
		"stream",
		5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}

	release, err := service.AcquireAgentStream(
		context.Background(),
		"a2a",
		principal.ID,
		issued.Credential.ID,
		8,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcquireAgentStream(
		context.Background(),
		"a2a",
		principal.ID,
		issued.Credential.ID,
		8,
	); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("second stream error=%v, want concurrency limit", err)
	}

	guard.mu.Lock()
	requests := append([]AgentExecutionGuardRequest(nil), guard.requests...)
	guard.mu.Unlock()
	if len(requests) != 5 {
		// First acquisition reaches all three dimensions. The rejected second
		// acquisition reserves/releases global, then fails at principal.
		t.Fatalf("guard requests=%d, want 5: %#v", len(requests), requests)
	}
	for _, request := range requests {
		if request.RateLimit != 0 {
			t.Fatalf("stream dimension consumed request-rate quota: %+v", request)
		}
	}
	if requests[0].SubjectID != "stream:a2a:global" ||
		requests[1].SubjectID != "stream:a2a:principal:"+principal.ID ||
		requests[2].SubjectID != "stream:a2a:credential:"+principal.ID+":"+issued.Credential.ID {
		t.Fatalf("unexpected stream dimensions: %#v", requests[:3])
	}

	release()
	release()
	guard.mu.Lock()
	releases := guard.releases
	guard.mu.Unlock()
	if releases != 4 {
		// Three permits from the first stream plus the partial global permit
		// released while rejecting the second stream.
		t.Fatalf("guard releases=%d, want 4", releases)
	}
	if nextRelease, err := service.AcquireAgentStream(
		context.Background(),
		"a2a",
		principal.ID,
		issued.Credential.ID,
		8,
	); err != nil {
		t.Fatalf("released stream capacity was not reusable: %v", err)
	} else {
		nextRelease()
	}
}

func TestAcquireAgentStreamReleasesPartialDimensionsWhenCredentialAdmissionFails(t *testing.T) {
	db := openAgentNativeTestDB(t)
	guard := newRecordingAgentStreamGuard()
	guard.failAt = 3
	service := NewAgentNativeService(db, AgentNativeOptions{
		CredentialPepper: []byte("stream-partial-test-pepper"),
		ExecutionGuard:   guard,
	})
	principal, err := service.CreateServicePrincipal(context.Background(), CreateServicePrincipalInput{
		Name:               "stream-partial-agent",
		Scopes:             []string{models.ScopeTasksManage},
		RateLimitPerMinute: 100,
		ConcurrentLimit:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueCredential(
		context.Background(),
		principal.ID,
		"stream",
		5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcquireAgentStream(
		context.Background(),
		"a2a",
		principal.ID,
		issued.Credential.ID,
		8,
	); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("credential admission error=%v", err)
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.releases != 2 {
		t.Fatalf("partial admission releases=%d, want 2", guard.releases)
	}
}

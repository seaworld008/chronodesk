package a2a

import (
	"fmt"
	"sync"
	"testing"
)

func TestTaskLocksRemainBoundedAcrossUntrustedIdentifiers(t *testing.T) {
	service := NewService(nil, nil, ServiceOptions{})
	first := service.taskLock("task-stable")
	if first != service.taskLock("task-stable") {
		t.Fatal("the same task identifier did not use a stable lock shard")
	}

	locks := make(map[*syncMutexAlias]struct{})
	for index := 0; index < 100_000; index++ {
		lock := service.taskLock(fmt.Sprintf("untrusted-task-%d", index))
		locks[(*syncMutexAlias)(lock)] = struct{}{}
	}
	if len(locks) > taskLockShardCount {
		t.Fatalf("unique task lock shards = %d, want at most %d", len(locks), taskLockShardCount)
	}
	if len(locks) < taskLockShardCount/2 {
		t.Fatalf("task identifiers used only %d lock shards; hash distribution is unexpectedly poor", len(locks))
	}
}

// A local alias lets the test use mutex pointers as map keys without exposing
// any test-only method from Service.
type syncMutexAlias = sync.Mutex

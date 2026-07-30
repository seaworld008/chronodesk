package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
)

var ErrExternalIOInsideProjectTransaction = errors.New(
	"external I/O is forbidden inside a project database transaction",
)

// requireExternalIOOutsideProjectTransaction is a fail-closed production
// boundary for model, search, object-storage, Redis and outbound-network
// adapters. Callers must claim/snapshot state in a short transaction, perform
// I/O after it commits, then open a second short transaction to revalidate
// live authorization and atomically finalize domain state.
func requireExternalIOOutsideProjectTransaction(
	ctx context.Context,
	operation string,
) error {
	if ctx == nil {
		return fmt.Errorf(
			"%w: %s context is required",
			ErrExternalIOInsideProjectTransaction,
			strings.TrimSpace(operation),
		)
	}
	if !scopeddb.HasTransaction(ctx) {
		return nil
	}
	return fmt.Errorf(
		"%w: %s",
		ErrExternalIOInsideProjectTransaction,
		strings.TrimSpace(operation),
	)
}

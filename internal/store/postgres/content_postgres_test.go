package postgres

import (
	"context"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func TestContentStore_OpenReturnsCapabilityError(t *testing.T) {
	cs := &ContentStore{}
	err := cs.Open(context.Background(), adapter.ContentConfig{DSN: "postgres://localhost/memora"})
	if err == nil {
		t.Fatal("expected ErrCapability from stub postgres ContentStore")
	}
	if !isCapabilityError(err) {
		t.Fatalf("expected ErrCapability, got: %v", err)
	}
}

func TestContentStore_MethodsReturnCapabilityError(t *testing.T) {
	cs := &ContentStore{}
	ctx := context.Background()

	if err := cs.PutMemoryContent(ctx, "ws", "mem", "md5", "body"); err != types.ErrCapability {
		t.Fatalf("PutMemoryContent: expected ErrCapability, got %v", err)
	}
	if _, err := cs.GetMemoryContent(ctx, "ws", "mem"); err != types.ErrCapability {
		t.Fatalf("GetMemoryContent: expected ErrCapability, got %v", err)
	}
	if _, err := cs.GetCellContentBatch(ctx, "ws", "mem", []string{"c1"}); err != types.ErrCapability {
		t.Fatalf("GetCellContentBatch: expected ErrCapability, got %v", err)
	}
	if err := cs.DeleteAllForMemory(ctx, "ws", "mem"); err != types.ErrCapability {
		t.Fatalf("DeleteAllForMemory: expected ErrCapability, got %v", err)
	}
}

func isCapabilityError(err error) bool {
	for err != nil {
		if err == types.ErrCapability {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return false
}

package services

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLocalAttachmentStorageNormalLifecycle(t *testing.T) {
	root := t.TempDir()
	storage, err := NewLocalAttachmentStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	stored, err := storage.Put(
		ctx,
		"tickets/42/evidence.txt",
		strings.NewReader("trusted attachment bytes"),
		1024,
	)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if stored.Key != "tickets/42/evidence.txt" ||
		stored.Size != int64(len("trusted attachment bytes")) {
		t.Fatalf("stored=%+v", stored)
	}
	reader, err := storage.Open(ctx, stored.Key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	body, err := io.ReadAll(reader)
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatalf("close attachment: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	if string(body) != "trusted attachment bytes" {
		t.Fatalf("body=%q", body)
	}
	if err := storage.Delete(ctx, stored.Key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(
		root,
		"tickets",
		"42",
		"evidence.txt",
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted attachment stat error=%v", err)
	}

	staged, err := storage.Stage(
		ctx,
		".staging/upload-token.bin",
		bytes.NewBufferString("staged bytes"),
		1024,
	)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	stagedReader, err := storage.OpenStaged(ctx, staged.Key)
	if err != nil {
		t.Fatalf("OpenStaged: %v", err)
	}
	stagedBody, err := io.ReadAll(stagedReader)
	if closeErr := stagedReader.Close(); closeErr != nil {
		t.Fatalf("close staged attachment: %v", closeErr)
	}
	if err != nil || string(stagedBody) != "staged bytes" {
		t.Fatalf("staged body=%q err=%v", stagedBody, err)
	}
	if err := storage.DeleteStaged(ctx, staged.Key); err != nil {
		t.Fatalf("DeleteStaged: %v", err)
	}
}

func TestLocalAttachmentStorageRejectsTraversalAndSeparatorVariants(
	t *testing.T,
) {
	root := t.TempDir()
	storage, err := NewLocalAttachmentStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	keys := []string{
		"",
		".",
		"..",
		"../escape.txt",
		"tickets/../../escape.txt",
		"./escape.txt",
		"tickets//escape.txt",
		"tickets/./escape.txt",
		"tickets/",
		`tickets\..\escape.txt`,
		`C:\escape.txt`,
		"C:/escape.txt",
		`\\server\share\escape.txt`,
		" tickets/escape.txt",
		"tickets/escape.txt ",
		"tickets/\x00escape.txt",
		filepath.Join(root, "absolute.txt"),
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			if _, err := storage.Put(
				ctx,
				key,
				strings.NewReader("must not be written"),
				1024,
			); !errors.Is(err, ErrInvalidAttachmentName) {
				t.Fatalf("Put(%q) error=%v", key, err)
			}
			if _, err := storage.Open(
				ctx,
				key,
			); !errors.Is(err, ErrInvalidAttachmentName) {
				t.Fatalf("Open(%q) error=%v", key, err)
			}
			if err := storage.Delete(
				ctx,
				key,
			); !errors.Is(err, ErrInvalidAttachmentName) {
				t.Fatalf("Delete(%q) error=%v", key, err)
			}
		})
	}

	for _, key := range []string{
		"../staged.bin",
		"/.staging/staged.bin",
		".staging/../staged.bin",
		`.staging\staged.bin`,
		".staging/nested/staged.bin",
		".staging/C:/staged.bin",
	} {
		t.Run("stage/"+key, func(t *testing.T) {
			if _, err := storage.Stage(
				ctx,
				key,
				strings.NewReader("must not be staged"),
				1024,
			); !errors.Is(err, ErrInvalidAttachmentName) {
				t.Fatalf("Stage(%q) error=%v", key, err)
			}
		})
	}
}

func TestLocalAttachmentStorageRejectsSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	storage, err := NewLocalAttachmentStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	victimPath := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victimPath, []byte("outside victim"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	if _, err := storage.Open(ctx, "escape/victim.txt"); err == nil {
		t.Fatal("Open followed a directory symlink outside the root")
	}
	if err := storage.Delete(ctx, "escape/victim.txt"); err == nil {
		t.Fatal("Delete followed a directory symlink outside the root")
	}
	if _, err := storage.Put(
		ctx,
		"escape/new.txt",
		strings.NewReader("escape"),
		1024,
	); err == nil {
		t.Fatal("Put followed a directory symlink outside the root")
	}
	assertLocalAttachmentOutsideFile(
		t,
		victimPath,
		"outside victim",
	)
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("outside new file stat error=%v", err)
	}

	ticketsPath := filepath.Join(root, "tickets")
	if err := os.MkdirAll(ticketsPath, 0o700); err != nil {
		t.Fatal(err)
	}
	finalLink := filepath.Join(ticketsPath, "final.txt")
	if err := os.Symlink(victimPath, finalLink); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Open(ctx, "tickets/final.txt"); err == nil {
		t.Fatal("Open followed a final symlink outside the root")
	}
	if err := storage.Delete(ctx, "tickets/final.txt"); err == nil {
		t.Fatal("Delete accepted a final symlink")
	}
	if _, err := storage.Put(
		ctx,
		"tickets/final.txt",
		strings.NewReader("replacement"),
		1024,
	); err == nil {
		t.Fatal("Put accepted a final symlink")
	}
	assertLocalAttachmentOutsideFile(
		t,
		victimPath,
		"outside victim",
	)

	partialLink := filepath.Join(ticketsPath, "partial.txt.partial")
	if err := os.Symlink(victimPath, partialLink); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Put(
		ctx,
		"tickets/partial.txt",
		strings.NewReader("replacement"),
		1024,
	); err == nil {
		t.Fatal("Put accepted a symlink at its partial-file path")
	}
	assertLocalAttachmentOutsideFile(
		t,
		victimPath,
		"outside victim",
	)

	stagingLink := filepath.Join(root, ".staging")
	if err := os.Symlink(outside, stagingLink); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Stage(
		ctx,
		".staging/staged.bin",
		strings.NewReader("staged escape"),
		1024,
	); err == nil {
		t.Fatal("Stage followed a staging-directory symlink outside the root")
	}
	if _, err := os.Stat(filepath.Join(outside, "staged.bin")); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("outside staged file stat error=%v", err)
	}
}

func TestLocalAttachmentStorageRejectsNamedPipeWithoutBlocking(
	t *testing.T,
) {
	root := t.TempDir()
	storage, err := NewLocalAttachmentStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(
		filepath.Join(root, "tickets"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(
		filepath.Join(root, "tickets", "blocked.pipe"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		reader, openErr := storage.Open(
			context.Background(),
			"tickets/blocked.pipe",
		)
		if reader != nil {
			_ = reader.Close()
		}
		result <- openErr
	}()
	select {
	case openErr := <-result:
		if !errors.Is(openErr, ErrInvalidAttachmentName) {
			t.Fatalf("Open(named pipe) error=%v", openErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Open(named pipe) blocked")
	}
}

func TestLocalAttachmentStorageRootHandleStopsDirectorySwapEscape(
	t *testing.T,
) {
	root := t.TempDir()
	outside := t.TempDir()
	storage, err := NewLocalAttachmentStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	reader := &localAttachmentDirectorySwapReader{
		body: []byte("raced bytes"),
		swap: func() error {
			if err := os.Rename(
				filepath.Join(root, "tickets"),
				filepath.Join(root, "parked"),
			); err != nil {
				return err
			}
			return os.Symlink(outside, filepath.Join(root, "tickets"))
		},
	}

	if _, err := storage.Put(
		context.Background(),
		"tickets/race.txt",
		reader,
		1024,
	); err == nil {
		t.Fatal("Put escaped after its parent directory was swapped")
	}
	if reader.swapErr != nil {
		t.Fatalf("set up directory swap: %v", reader.swapErr)
	}
	if _, err := os.Stat(filepath.Join(outside, "race.txt")); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("outside raced file stat error=%v", err)
	}
}

type localAttachmentDirectorySwapReader struct {
	body    []byte
	swap    func() error
	swapErr error
	read    bool
}

func (reader *localAttachmentDirectorySwapReader) Read(buffer []byte) (
	int,
	error,
) {
	if reader.read {
		return 0, io.EOF
	}
	reader.read = true
	reader.swapErr = reader.swap()
	if reader.swapErr != nil {
		return 0, reader.swapErr
	}
	return copy(buffer, reader.body), io.EOF
}

func assertLocalAttachmentOutsideFile(
	t *testing.T,
	path string,
	want string,
) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read outside sentinel: %v", err)
	}
	if string(body) != want {
		t.Fatalf("outside sentinel=%q, want %q", body, want)
	}
}

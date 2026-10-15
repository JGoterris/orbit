package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	a, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if ok, err := TryLockFile(a); !ok || err != nil {
		t.Fatalf("first lock = %v, %v", ok, err)
	}
	if ok, err := TryLockFile(b); ok || err != nil {
		t.Fatalf("second open file got the lock: %v, %v", ok, err)
	}
	// the holder can still be read (on Windows the lock sits past the end of the file)
	if err := os.WriteFile(path, []byte("123"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "123" {
		t.Fatalf("read while locked = %q, %v", data, err)
	}
	if err := UnlockFile(a); err != nil {
		t.Fatal(err)
	}
	if ok, err := TryLockFile(b); !ok || err != nil {
		t.Fatalf("lock after unlock = %v, %v", ok, err)
	}
}

package pathsanitize

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSafeJoinAllowsInsidePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "world", "region"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []string{
		"server.properties",
		"world/region/r.0.0.mca",
		"plugins/Essentials/config.yml",
		"./nested/./file.txt",
		"",
		".",
	}
	for _, name := range cases {
		got, err := SafeJoin(root, name)
		if err != nil {
			t.Fatalf("SafeJoin(%q): %v", name, err)
		}
		if !within(root, got) {
			t.Fatalf("SafeJoin(%q) = %q, outside root", name, got)
		}
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	root := t.TempDir()

	cases := []string{
		"../outside.txt",
		"..",
		"a/../../outside.txt",
		"world/../../../etc/passwd",
	}
	for _, name := range cases {
		if _, err := SafeJoin(root, name); !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("SafeJoin(%q) error = %v, want ErrOutsideRoot", name, err)
		}
	}
}

func TestSafeJoinRejectsSymlinkToFileOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "leak")); err != nil {
		t.Fatal(err)
	}

	if _, err := SafeJoin(root, "leak"); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("symlink to file outside root: err = %v, want ErrSymlinkEscape", err)
	}
	// Even as a read target nested under a valid-looking name.
	if _, err := SafeJoin(root, "leak"); err == nil {
		t.Fatal("expected error for symlinked file")
	}
}

func TestSafeJoinRejectsSymlinkedDirectoryOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}

	if _, err := SafeJoin(root, "out/file.txt"); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("symlinked dir: err = %v, want ErrSymlinkEscape", err)
	}
}

func TestSafeJoinRejectsSymlinkedSubdirEscape(t *testing.T) {
	root := t.TempDir()
	world := filepath.Join(root, "world")
	if err := os.MkdirAll(world, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(world, "saves")); err != nil {
		t.Fatal(err)
	}

	if _, err := SafeJoin(root, filepath.Join("world", "saves", "level.dat")); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("nested symlink escape: err = %v, want ErrSymlinkEscape", err)
	}
}

func TestSafeJoinAllowsNewFileUnderSymlinkedInsideDir(t *testing.T) {
	root := t.TempDir()
	// A symlink pointing *inside* root is legitimate (e.g. world -> worlds/main).
	if err := os.MkdirAll(filepath.Join(root, "worlds", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "worlds", "main"), filepath.Join(root, "world")); err != nil {
		t.Fatal(err)
	}

	got, err := SafeJoin(root, filepath.Join("world", "level.dat"))
	if err != nil {
		t.Fatalf("new file under inside-root symlink: %v", err)
	}
	if !within(root, got) {
		t.Fatalf("got %q outside root", got)
	}
}

func TestSafeJoinAllowsNewFileInExistingDir(t *testing.T) {
	root := t.TempDir()
	got, err := SafeJoin(root, "plugins/newly-created.jar")
	if err != nil {
		t.Fatalf("SafeJoin: %v", err)
	}
	if want := filepath.Join(root, "plugins", "newly-created.jar"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

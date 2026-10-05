// Package pathsanitize keeps every filesystem path confined to a root
// directory. It defends against lexical traversal ("../") and symlink
// escapes. All panel and daemon code that touches server files must resolve
// user-supplied paths through SafeJoin before touching the filesystem.
package pathsanitize

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrOutsideRoot means the path escapes root lexically ("../").
	ErrOutsideRoot = errors.New("path escapes the allowed root")
	// ErrSymlinkEscape means the path resolves through a symlink outside root.
	ErrSymlinkEscape = errors.New("path resolves to a location outside the allowed root via symlink")
)

// SafeJoin resolves name within root and returns the cleaned path. It rejects
// paths that escape root lexically and paths that resolve (through symlinks)
// outside root. The target itself may not exist yet (file creation); its
// deepest existing ancestor is resolved instead.
func SafeJoin(root, name string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}

	target := filepath.Clean(filepath.Join(rootAbs, name))
	if !within(rootAbs, target) {
		return "", fmt.Errorf("%w: %q", ErrOutsideRoot, name)
	}

	real, err := resolveExisting(target)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve root symlinks: %w", err)
	}
	if !within(realRoot, real) {
		return "", fmt.Errorf("%w: %q", ErrSymlinkEscape, name)
	}
	return target, nil
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(os.PathSeparator))
}

// resolveExisting evaluates symlinks on the longest existing prefix of p and
// re-appends the remainder, so not-yet-created files inside symlinked
// directories are still validated.
func resolveExisting(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err == nil {
		return real, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	dir, rest := filepath.Split(filepath.Clean(p))
	parent := filepath.Clean(dir)
	for {
		real, err := filepath.EvalSymlinks(parent)
		if err == nil {
			return filepath.Join(real, rest), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if parent == filepath.Dir(parent) {
			return "", fmt.Errorf("%w while resolving %q", os.ErrNotExist, p)
		}
		rest = filepath.Join(filepath.Base(parent), rest)
		parent = filepath.Dir(parent)
	}
}

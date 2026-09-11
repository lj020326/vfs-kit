package vfs

import (
	"errors"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newContainedFS builds a VFS rooted at <tmp>/root and drops a secret file in
// <tmp> (i.e. one level *above* the VFS root) so escapes are observable.
func newContainedFS(t *testing.T) (v VFS, base, root string) {
	t.Helper()
	base = t.TempDir()
	root = filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "secret.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := FS(root)
	if err != nil {
		t.Fatal(err)
	}
	return v, base, root
}

// TestOpenRejectsTraversal is the regression test for the read-side traversal:
// Open used to Join the root with an uncleaned relative path, so ".." escaped.
func TestOpenRejectsTraversal(t *testing.T) {
	v, _, _ := newContainedFS(t)

	for _, p := range []string{"../secret.txt", "sub/../../secret.txt", "..", "../"} {
		f, err := v.Open(p)
		if err == nil {
			b, _ := io.ReadAll(f)
			_ = f.Close()
			t.Fatalf("Open(%q) escaped the VFS root and read %q", p, string(b))
		}
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Open(%q) = %v, want ErrInvalidPath", p, err)
		}
	}
}

// TestEveryEntryPointRejectsTraversal pins the property the fix is about: no
// entry point may accept a path that another one rejects.
func TestEveryEntryPointRejectsTraversal(t *testing.T) {
	v, _, _ := newContainedFS(t)
	const esc = "../secret.txt"

	ops := map[string]func() error{
		"Open":     func() error { _, err := v.Open(esc); return err },
		"OpenFile": func() error { _, err := v.OpenFile(esc, os.O_RDONLY, 0o644); return err },
		"Stat":     func() error { _, err := v.Stat(esc); return err },
		"Lstat":    func() error { _, err := v.Lstat(esc); return err },
		"Mkdir":    func() error { return v.Mkdir("../escaped", 0o755) },
		"Remove":   func() error { return v.Remove(esc) },
	}
	for name, op := range ops {
		if err := op(); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%s(%q) = %v, want ErrInvalidPath", name, esc, err)
		}
	}
	if _, err := os.Stat(filepath.Join(t.TempDir(), "escaped")); err == nil {
		t.Error("Mkdir created a directory outside the VFS root")
	}
}

// TestLegitimatePathsStillWork guards against the fix being too aggressive.
func TestLegitimatePathsStillWork(t *testing.T) {
	v, _, _ := newContainedFS(t)

	for _, p := range []string{"inside.txt", "/inside.txt", "./inside.txt", "sub/../inside.txt"} {
		f, err := v.Open(p)
		if err != nil {
			t.Fatalf("Open(%q) = %v, want success", p, err)
		}
		b, _ := io.ReadAll(f)
		_ = f.Close()
		if string(b) != "inside" {
			t.Errorf("Open(%q) read %q, want %q", p, string(b), "inside")
		}
	}
}

// TestDotDotIsSegmentWise: ".." must only match a whole path segment, so file
// names that merely contain two dots stay usable.
func TestDotDotIsSegmentWise(t *testing.T) {
	v, _, root := newContainedFS(t)

	for _, name := range []string{"v1..2.json", "..hidden", "a..b"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
		f, err := v.Open(name)
		if err != nil {
			t.Errorf("Open(%q) = %v, want success (\"..\" is not a whole segment)", name, err)
			continue
		}
		_ = f.Close()
	}
	if !containsDotDot(filepath.Clean("a/../../b")) {
		t.Error(`containsDotDot("../b") = false, want true`)
	}
	if containsDotDot("v1..2.json") {
		t.Error(`containsDotDot("v1..2.json") = true, want false`)
	}
}

// TestChrootContainsDotDot is the regression test for Chroot being a plain
// string prefix: "../.." used to climb back out into the parent VFS.
func TestChrootContainsDotDot(t *testing.T) {
	v, _, root := newContainedFS(t)
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rootonly.txt"), []byte("parent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "ok.txt"), []byte("chrooted"), 0o644); err != nil {
		t.Fatal(err)
	}

	cr, err := Chroot("/sub", v)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cr.Stat("../rootonly.txt"); err == nil {
		t.Error("Chroot Stat(\"../rootonly.txt\") escaped the chroot")
	}
	if _, err := cr.Open("../../secret.txt"); err == nil {
		t.Error("Chroot Open(\"../../secret.txt\") escaped the chroot")
	}
	// Inside the chroot everything still resolves normally.
	f, err := cr.Open("ok.txt")
	if err != nil {
		t.Fatalf("Chroot Open(\"ok.txt\") = %v, want success", err)
	}
	b, _ := io.ReadAll(f)
	_ = f.Close()
	if string(b) != "chrooted" {
		t.Errorf("Chroot Open(\"ok.txt\") read %q, want %q", string(b), "chrooted")
	}
}

// TestErrorsDoNotLeakHostRoot: error strings should name the VFS path, not the
// host path, and must stay classifiable by errors.Is.
func TestErrorsDoNotLeakHostRoot(t *testing.T) {
	v, _, root := newContainedFS(t)

	_, err := v.Open("missing.txt")
	if err == nil {
		t.Fatal("Open(missing.txt) succeeded, want error")
	}
	if strings.Contains(err.Error(), root) {
		t.Errorf("error leaks host root: %v", err)
	}
	if !errors.Is(err, iofs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false for %v; classification must survive rewriting", err)
	}
	if !os.IsNotExist(err) {
		t.Errorf("os.IsNotExist = false for %v", err)
	}

	if _, err := v.Stat("missing.txt"); !errors.Is(err, iofs.ErrNotExist) {
		t.Errorf("Stat: errors.Is(err, fs.ErrNotExist) = false for %v", err)
	}
	if err := v.Remove("missing.txt"); !errors.Is(err, iofs.ErrNotExist) {
		t.Errorf("Remove: errors.Is(err, fs.ErrNotExist) = false for %v", err)
	}
	if err := v.Mkdir("missing/deep", 0o755); err == nil {
		t.Error("Mkdir(missing/deep) succeeded, want error")
	}
}

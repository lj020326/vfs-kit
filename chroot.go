package vfs

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
)

type chrootFileSystem struct {
	root string
	fs   VFS
}

// path maps a path inside the chroot to a path in the underlying VFS.
//
// The incoming path is first anchored to "/" and cleaned, so any ".." segments
// are resolved against the chroot root and collapse at it (exactly as they do
// at the root of a real filesystem). Without this the chroot was a plain string
// prefix and "../.." climbed straight back out of it.
func (fs *chrootFileSystem) path(p string) string {
	return path.Join(fs.root, path.Clean("/"+filepath.ToSlash(p)))
}

func (fs *chrootFileSystem) VFS() VFS {
	return fs.fs
}

func (fs *chrootFileSystem) Open(path string) (RFile, error) {
	return fs.fs.Open(fs.path(path))
}

func (fs *chrootFileSystem) OpenFile(path string, flag int, perm os.FileMode) (WFile, error) {
	return fs.fs.OpenFile(fs.path(path), flag, perm)
}

func (fs *chrootFileSystem) Lstat(path string) (os.FileInfo, error) {
	return fs.fs.Lstat(fs.path(path))
}

func (fs *chrootFileSystem) Stat(path string) (os.FileInfo, error) {
	return fs.fs.Stat(fs.path(path))
}

func (fs *chrootFileSystem) ReadDir(path string) ([]os.FileInfo, error) {
	return fs.fs.ReadDir(fs.path(path))
}

func (fs *chrootFileSystem) Mkdir(path string, perm os.FileMode) error {
	return fs.fs.Mkdir(fs.path(path), perm)
}

func (fs *chrootFileSystem) Remove(path string) error {
	return fs.fs.Remove(fs.path(path))
}

func (fs *chrootFileSystem) String() string {
	return fmt.Sprintf("Chroot %s %s", fs.root, fs.fs.String())
}

// Chroot returns a new VFS wrapping the given VFS, making the given
// directory the new root ("/"). Note that root must be an existing
// directory in the given file system, otherwise an error is returned.
func Chroot(root string, fs VFS) (VFS, error) {
	root = path.Clean("/" + root)
	st, err := fs.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	return &chrootFileSystem{root: root + "/", fs: fs}, nil
}

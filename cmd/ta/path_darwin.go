package main

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"
)

// canonicalPath returns the path macOS stores for dir, resolving symlinks and
// letter case: /tmp/x and /users/me/src both become /private/tmp/x and
// /Users/me/src, so one directory always maps to one vault.
func canonicalPath(dir string) (string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf [1024]byte // MAXPATHLEN
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_GETPATH, uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		return "", &os.PathError{Op: "fcntl F_GETPATH", Path: dir, Err: errno}
	}
	n := bytes.IndexByte(buf[:], 0)
	if n < 0 {
		n = len(buf)
	}
	return string(buf[:n]), nil
}

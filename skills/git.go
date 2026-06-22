package skills

import (
	"os/exec"
	"strconv"
	"strings"
)

// Minimal git plumbing for pinning/comparing skills against upstream. Kept here
// rather than reusing ghq's VCSBackend so the skills feature stays self-contained.

// Head returns the current commit SHA of the clone at dir.
func Head(dir string) (string, error) {
	return gitOut(dir, "rev-parse", "HEAD")
}

// Fetch updates remote-tracking refs without touching the working tree.
func Fetch(dir string) error {
	_, err := gitOut(dir, "fetch", "--quiet")
	return err
}

// RemoteHead returns the SHA the tracked upstream branch points at, falling
// back to origin/HEAD when no upstream is configured.
func RemoteHead(dir string) (string, error) {
	if s, err := gitOut(dir, "rev-parse", "@{u}"); err == nil {
		return s, nil
	}
	return gitOut(dir, "rev-parse", "origin/HEAD")
}

// CountRange returns how many commits are in a..b.
func CountRange(dir, a, b string) (int, error) {
	s, err := gitOut(dir, "rev-list", "--count", a+".."+b)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

// Checkout moves the clone to ref.
func Checkout(dir, ref string) error {
	_, err := gitOut(dir, "checkout", "--quiet", ref)
	return err
}

func gitOut(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

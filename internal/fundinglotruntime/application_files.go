package fundinglotruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"golang.org/x/sys/unix"
)

var applicationSecretName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var applicationManifestName = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]{0,127}$`)
var applicationHashText = regexp.MustCompile(`^[0-9a-f]{64}$`)

type applicationPrivateDirectory struct{ fd int }

func applicationOpenPrivate(b applicationBudget, path string) (*applicationPrivateDirectory, string, error) {
	if b.check() != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || !applicationManifestName.MatchString(filepath.Base(path)) || b.check() != nil {
		return nil, "", ErrRuntime
	}
	fd, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", ErrRuntime
	}
	if b.check() != nil {
		_ = unix.Close(fd)
		return nil, "", ErrRuntime
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || b.check() != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&07777 != 0700 {
		_ = unix.Close(fd)
		return nil, "", ErrRuntime
	}
	return &applicationPrivateDirectory{fd: fd}, filepath.Base(path), nil
}

func (d *applicationPrivateDirectory) close() error {
	if d == nil || d.fd < 0 {
		return nil
	}
	fd := d.fd
	d.fd = -1
	if unix.Close(fd) != nil {
		return ErrRuntime
	}
	return nil
}

func (d *applicationPrivateDirectory) read(b applicationBudget, name string, limit int) (raw []byte, err error) {
	if b.check() != nil || d == nil || d.fd < 0 || !applicationManifestName.MatchString(name) || limit < 1 || b.check() != nil {
		return nil, ErrRuntime
	}
	fd, openErr := unix.Openat(d.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if openErr != nil {
		return nil, ErrRuntime
	}
	f := os.NewFile(uintptr(fd), "funding-lot-application-private")
	if f == nil {
		_ = unix.Close(fd)
		return nil, ErrRuntime
	}
	defer func() {
		if f.Close() != nil || b.check() != nil {
			raw, err = nil, ErrRuntime
		}
	}()
	if b.check() != nil {
		return nil, ErrRuntime
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || b.check() != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Size < 1 || st.Size > int64(limit) {
		return nil, ErrRuntime
	}
	if b.check() != nil {
		return nil, ErrRuntime
	}
	raw, err = io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || b.check() != nil || len(raw) == 0 || len(raw) > limit {
		return nil, ErrRuntime
	}
	return raw, nil
}

func (d *applicationPrivateDirectory) secret(b applicationBudget, spec applicationSecretFile, limit int) ([]byte, error) {
	if b.check() != nil || !applicationSecretName.MatchString(spec.Name) || !applicationHashText.MatchString(spec.SHA256) || b.check() != nil {
		return nil, ErrRuntime
	}
	raw, err := d.read(b, spec.Name, limit)
	if err != nil || b.check() != nil {
		return nil, ErrRuntime
	}
	h := sha256.Sum256(raw)
	if hex.EncodeToString(h[:]) != spec.SHA256 || b.check() != nil {
		return nil, ErrRuntime
	}
	return raw, nil
}

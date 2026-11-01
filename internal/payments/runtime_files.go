package payments

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"golang.org/x/sys/unix"
)

var runtimeSecretName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var runtimeManifestName = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]{0,127}$`)
var runtimeHashText = regexp.MustCompile(`^[0-9a-f]{64}$`)

type runtimePrivateDirectory struct{ fd int }

func runtimeOpenPrivate(path string) (*runtimePrivateDirectory, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !runtimeManifestName.MatchString(filepath.Base(path)) {
		return nil, "", ErrPaymentsConfiguration
	}
	fd, e := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, "", ErrPaymentsConfiguration
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&07777 != 0700 {
		unix.Close(fd)
		return nil, "", ErrPaymentsConfiguration
	}
	return &runtimePrivateDirectory{fd}, filepath.Base(path), nil
}
func (d *runtimePrivateDirectory) close() { _ = unix.Close(d.fd) }
func (d *runtimePrivateDirectory) read(name string, limit int) ([]byte, error) {
	if !runtimeManifestName.MatchString(name) {
		return nil, ErrPaymentsConfiguration
	}
	fd, e := unix.Openat(d.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, ErrPaymentsConfiguration
	}
	f := os.NewFile(uintptr(fd), "payment-runtime-secret")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Size < 1 || st.Size > int64(limit) {
		return nil, ErrPaymentsConfiguration
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil || len(b) == 0 || len(b) > limit {
		return nil, ErrPaymentsConfiguration
	}
	return b, nil
}
func (d *runtimePrivateDirectory) secret(spec runtimeSecretFile, limit int) ([]byte, error) {
	if !runtimeSecretName.MatchString(spec.Name) || !runtimeHashText.MatchString(spec.SHA256) {
		return nil, ErrPaymentsConfiguration
	}
	b, e := d.read(spec.Name, limit)
	if e != nil {
		return nil, e
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != spec.SHA256 {
		return nil, ErrPaymentsConfiguration
	}
	return b, nil
}

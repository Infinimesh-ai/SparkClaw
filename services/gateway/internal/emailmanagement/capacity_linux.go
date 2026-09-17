//go:build linux

package emailmanagement

import "syscall"

var statfs = func(path string) (total, free int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	// Bavail, not Bfree: blocks reserved for root are not usable capacity.
	return int64(st.Blocks) * st.Bsize, int64(st.Bavail) * st.Bsize, nil
}

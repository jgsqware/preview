//go:build unix

package builtin

import "syscall"

// diskUsage : (total, libre) en octets, comme shutil.disk_usage.
func diskUsage(p string) (float64, float64, bool) {
	var st syscall.Statfs_t
	if syscall.Statfs(p, &st) != nil {
		return 0, 0, false
	}
	bs := float64(st.Bsize)
	total := float64(st.Blocks) * bs
	used := (float64(st.Blocks) - float64(st.Bfree)) * bs
	return total, total - used, true
}

//go:build !unix

package builtin

func diskUsage(p string) (float64, float64, bool) { return 0, 0, false }

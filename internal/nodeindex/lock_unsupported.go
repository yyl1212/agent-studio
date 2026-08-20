//go:build !darwin && !linux

package nodeindex

func tryRefreshLock(string) (func() error, error) {
	return nil, coded(CodeRefreshUnsupported, "node index refresh is unsupported on this platform", nil)
}

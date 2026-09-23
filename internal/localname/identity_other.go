//go:build !darwin && !linux && !windows

package localname

func isLocald(int) bool     { return false }
func isAdvertiser(int) bool { return false }

//go:build !linux

package discover

// ListeningPorts is unsupported on this platform.
func ListeningPorts() ([]int, error) {
	return nil, ErrHostScanUnsupported
}

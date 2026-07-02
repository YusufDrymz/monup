//go:build linux

package discover

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ListeningPorts returns TCP ports with a listener bound to a
// non-loopback address, parsed from /proc/net/tcp and /proc/net/tcp6.
func ListeningPorts() ([]int, error) {
	seen := map[int]bool{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if err := scanProcNet(f, seen); err != nil {
			return nil, err
		}
	}
	ports := make([]int, 0, len(seen))
	for p := range seen {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports, nil
}

const (
	tcpListen  = "0A"
	loopbackV4 = "0100007F"
	loopbackV6 = "00000000000000000000000001000000"
)

func scanProcNet(path string, seen map[int]bool) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Scan() // header line
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[3] != tcpListen {
			continue
		}
		addr, portHex, ok := strings.Cut(fields[1], ":")
		if !ok || addr == loopbackV4 || addr == loopbackV6 {
			continue
		}
		port, err := strconv.ParseInt(portHex, 16, 32)
		if err != nil {
			continue
		}
		seen[int(port)] = true
	}
	return sc.Err()
}

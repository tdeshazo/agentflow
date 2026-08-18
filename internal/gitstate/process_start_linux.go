//go:build linux

package gitstate

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func processStartToken(pid int) (string, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", false
	}
	text := string(b)
	closeParen := strings.LastIndexByte(text, ')')
	if closeParen < 0 || closeParen+2 >= len(text) {
		return "", false
	}
	fields := strings.Fields(text[closeParen+2:])
	// The slice starts at stat field 3 (state); field 22 (starttime) is index 19.
	if len(fields) <= 19 || fields[0] == "Z" {
		return "", false
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", false
	}
	return fields[19], true
}

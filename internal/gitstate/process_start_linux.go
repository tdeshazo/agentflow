//go:build linux

package gitstate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

func processStartToken(pid int) (string, processInspection) {
	return processStartTokenWithRead(pid, os.ReadFile)
}

func processStartTokenWithRead(pid int, readFile func(string) ([]byte, error)) (string, processInspection) {
	b, err := readFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", processInspectionExited
		}
		return "", processInspectionUnavailable
	}
	text := string(b)
	closeParen := strings.LastIndexByte(text, ')')
	if closeParen < 0 || closeParen+2 >= len(text) {
		return "", processInspectionUnavailable
	}
	fields := strings.Fields(text[closeParen+2:])
	// The slice starts at stat field 3 (state); field 22 (starttime) is index 19.
	if len(fields) <= 19 {
		return "", processInspectionUnavailable
	}
	if fields[0] == "Z" {
		return "", processInspectionExited
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", processInspectionUnavailable
	}
	return fields[19], processInspectionIdentified
}

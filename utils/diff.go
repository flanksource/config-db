package utils

import (
	"bufio"
	"strings"
)

// IsReorderingDiff returns true if the diff only contains reordered lines.
func IsReorderingDiff(diff string) bool {
	scanner := bufio.NewScanner(strings.NewReader(diff))
	lines := make(map[string]struct{})

	// discard the headers of the diff.
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "@@") && strings.Contains(line, "@@") {
			break
		}
	}

	for scanner.Scan() {
		line := scanner.Text()

		// For each line addition, we try to find the corresponding line removal
		// and vice versa.
		// If all lines are paired, then it's a reordering change.

		if strings.HasPrefix(line, "+") {
			opposite := strings.Replace(line, "+", "-", 1)
			if _, ok := lines[opposite]; ok {
				delete(lines, opposite)
			} else {
				lines[line] = struct{}{}
			}
		}

		if strings.HasPrefix(line, "-") {
			opposite := strings.Replace(line, "-", "+", 1)
			if _, ok := lines[opposite]; ok {
				delete(lines, opposite)
			} else {
				lines[line] = struct{}{}
			}
		}

	}

	return len(lines) == 0
}

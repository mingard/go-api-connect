// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import "strings"

// Join joins a string map with a deliminator, filtering by a minimum string length.
func Join(delim string, min int, parts ...string) string {
	var filtered []string
	for _, part := range parts {
		if len(part) >= min {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, delim)
}

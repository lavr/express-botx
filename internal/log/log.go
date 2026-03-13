package log

import (
	"fmt"
	"os"
	"strings"
)

// Level controls verbosity: 0=silent, 1=verbose, 2=debug, 3=trace.
var Level int

// V prints to stderr if the current Level >= level.
func V(level int, format string, args ...any) {
	if Level >= level {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}

// V1 logs at verbose level (key steps).
func V1(format string, args ...any) { V(1, format, args...) }

// V2 logs at debug level (HTTP details).
func V2(format string, args ...any) { V(2, format, args...) }

// V3 logs at trace level (request/response bodies).
func V3(format string, args ...any) { V(3, format, args...) }

// Mask returns a masked version of a secret string.
// Shows first 4 and last 2 characters if long enough, otherwise "***".
func Mask(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "***" + s[len(s)-2:]
}

// MaskBearer masks a bearer token for logging.
func MaskBearer(token string) string {
	if len(token) <= 10 {
		return "***"
	}
	return token[:6] + "***"
}

// ParseVerbosity extracts -v/-vv/-vvv flags from args and returns
// the verbosity level and remaining args with the flags removed.
func ParseVerbosity(args []string) (int, []string) {
	level := 0
	var remaining []string

	for _, arg := range args {
		switch arg {
		case "-v":
			level = 1
		case "-vv":
			level = 2
		case "-vvv":
			level = 3
		default:
			if strings.HasPrefix(arg, "--verbose") {
				// --verbose=N or --verbose N handled via flag package
				remaining = append(remaining, arg)
			} else {
				remaining = append(remaining, arg)
			}
		}
	}

	return level, remaining
}

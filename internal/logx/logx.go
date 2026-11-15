// Package logx is a tiny leveled logger. The service runs in a container where stdout is the only
// place logs can go, so every line is written there with a level prefix.
package logx

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

// Level is a log severity.
type Level int

// Levels, from most to least verbose.
const (
	Debug Level = iota
	Info
	Warn
	Error
)

var (
	mu     sync.Mutex
	level  = Info
	logger = log.New(os.Stdout, "", log.LstdFlags)
)

// SetOutput redirects log lines, which tests use to capture them.
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	logger.SetOutput(w)
}

// SetLevel parses a level name. Unknown names keep the current level and return an error.
func SetLevel(name string) error {
	parsed, err := ParseLevel(name)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	level = parsed
	return nil
}

// ParseLevel turns "info" into Info.
func ParseLevel(name string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "info":
		return Info, nil
	case "debug":
		return Debug, nil
	case "warn", "warning":
		return Warn, nil
	case "error":
		return Error, nil
	}
	return Info, fmt.Errorf("unknown log level %q (debug, info, warn, error)", name)
}

func write(at Level, format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if at < level {
		return
	}
	label := [...]string{"DEBUG", "INFO", "WARN", "ERROR"}[at]
	logger.Printf("[%s] %s", label, fmt.Sprintf(format, args...))
}

// Debugf logs at debug level.
func Debugf(format string, args ...any) { write(Debug, format, args...) }

// Infof logs at info level.
func Infof(format string, args ...any) { write(Info, format, args...) }

// Warnf logs at warn level.
func Warnf(format string, args ...any) { write(Warn, format, args...) }

// Errorf logs at error level.
func Errorf(format string, args ...any) { write(Error, format, args...) }

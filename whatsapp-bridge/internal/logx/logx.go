// Package logx is a tiny leveled logger that writes to a file (with simple
// size-based rotation) and optionally to the console. It implements the
// whatsmeow logger interface.
package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

var levels = map[string]int{"DEBUG": 0, "INFO": 1, "WARN": 2, "ERROR": 3}

// Sink is the shared output.
type Sink struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	size    int64
	console io.Writer
	maxSize int64
}

// NewSink opens path for appending (path may be empty for console only).
func NewSink(path string, console io.Writer) (*Sink, error) {
	s := &Sink{path: path, console: console, maxSize: 20 << 20}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := s.open(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Sink) open() error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, _ := f.Stat()
	s.f = f
	if st != nil {
		s.size = st.Size()
	}
	return nil
}

func (s *Sink) write(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.console != nil {
		io.WriteString(s.console, line)
	}
	if s.f == nil {
		return
	}
	if s.size+int64(len(line)) > s.maxSize {
		s.f.Close()
		_ = os.Remove(s.path + ".1")
		_ = os.Rename(s.path, s.path+".1")
		if err := s.open(); err != nil {
			s.f = nil
			return
		}
		s.size = 0
	}
	n, _ := io.WriteString(s.f, line)
	s.size += int64(n)
}

// Close closes the file.
func (s *Sink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != nil {
		s.f.Close()
		s.f = nil
	}
}

// Logger is a named leveled logger.
type Logger struct {
	sink   *Sink
	module string
	min    int
}

// New returns a logger for module at the given minimum level.
func New(sink *Sink, module, level string) *Logger {
	l, ok := levels[strings.ToUpper(level)]
	if !ok {
		l = 1
	}
	return &Logger{sink: sink, module: module, min: l}
}

func (l *Logger) out(level, msg string, args ...any) {
	if levels[level] < l.min {
		return
	}
	l.sink.write(fmt.Sprintf("%s [%s %s] %s\n", time.Now().Format("2006-01-02 15:04:05.000"), l.module, level, fmt.Sprintf(msg, args...)))
}

func (l *Logger) Debugf(msg string, args ...any) { l.out("DEBUG", msg, args...) }
func (l *Logger) Infof(msg string, args ...any)  { l.out("INFO", msg, args...) }
func (l *Logger) Warnf(msg string, args ...any)  { l.out("WARN", msg, args...) }
func (l *Logger) Errorf(msg string, args ...any) { l.out("ERROR", msg, args...) }

// Sub returns a child logger.
func (l *Logger) Sub(module string) waLog.Logger {
	return &Logger{sink: l.sink, module: l.module + "/" + module, min: l.min}
}

var _ waLog.Logger = (*Logger)(nil)

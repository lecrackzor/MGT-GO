package utils

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger provides file and console logging with per-run rotation
type Logger struct {
	mu           sync.Mutex
	logFile      *os.File
	logDir       string
	logPath      string // Full path to current log file
	consoleLog   *log.Logger
	fileLog      *log.Logger
}

var globalLogger *Logger
var globalLoggerOnce sync.Once

// InitLogger initializes the global logger
func InitLogger(logDir string) error {
	var err error
	globalLoggerOnce.Do(func() {
		globalLogger, err = NewLogger(logDir)
	})
	return err
}

// GetLogger returns the global logger instance
func GetLogger() *Logger {
	if globalLogger == nil {
		// Initialize with default directory if not already initialized
		globalLoggerOnce.Do(func() {
			globalLogger, _ = NewLogger("./logs")
		})
	}
	return globalLogger
}

// NewLogger creates a new logger instance with per-run log file
func NewLogger(logDir string) (*Logger, error) {
	// Create logs directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	logger := &Logger{
		logDir: logDir,
	}

	// Create log file with timestamp (per-run)
	// Format: YYYY-MM-DD_HH-MM-SS.log
	now := time.Now()
	logFileName := fmt.Sprintf("%s.log", now.Format("2006-01-02_15-04-05"))
	logPath := filepath.Join(logDir, logFileName)
	
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	logger.logFile = file
	logger.logPath = logPath

	// Create file logger that writes to both file and console
	multiWriter := io.MultiWriter(os.Stdout, file)
	logger.fileLog = log.New(multiWriter, "", log.LstdFlags)

	// Create console logger (stdout)
	logger.consoleLog = log.New(os.Stdout, "", log.LstdFlags)

	return logger, nil
}

// rotateIfNeeded is a no-op for per-run logging (no rotation during runtime)
// This method is kept for compatibility but does nothing since we use per-run log files
func (l *Logger) rotateIfNeeded() error {
	// Per-run logging: no rotation needed during runtime
	// Each application run gets its own log file
	return nil
}

// Printf logs a formatted message to both console and file
func (l *Logger) Printf(format string, v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Ensure log file is still open
	if l.logFile == nil {
		// Fallback to console only if file is closed
		l.consoleLog.Printf(format, v...)
		return
	}

	// Write to both console and file
	l.fileLog.Printf(format, v...)
}

// Print logs a message to both console and file
func (l *Logger) Print(v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Ensure log file is still open
	if l.logFile == nil {
		// Fallback to console only if file is closed
		l.consoleLog.Print(v...)
		return
	}

	l.fileLog.Print(v...)
}

// Println logs a message with newline to both console and file
func (l *Logger) Println(v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Ensure log file is still open
	if l.logFile == nil {
		// Fallback to console only if file is closed
		l.consoleLog.Println(v...)
		return
	}

	l.fileLog.Println(v...)
}

// Close closes the log file
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.logFile != nil {
		return l.logFile.Close()
	}
	return nil
}

// Global convenience functions that use the global logger

// Logf logs using the global logger
func Logf(format string, v ...interface{}) {
	GetLogger().Printf(format, v...)
}

// Log logs using the global logger
func Log(v ...interface{}) {
	GetLogger().Print(v...)
}

// Logln logs using the global logger with newline
func Logln(v ...interface{}) {
	GetLogger().Println(v...)
}

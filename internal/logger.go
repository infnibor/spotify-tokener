package internal

import (
	"fmt"
	"log"
	"os"
	"time"
)

type LogLevel string

const (
	LogInfo  LogLevel = "INFO"
	LogWarn  LogLevel = "WARN"
	LogError LogLevel = "ERROR"
)

type Logger struct {
	verbose bool
}

func NewLogger() *Logger {
	return &Logger{
		verbose: os.Getenv("VERBOSE") == "true",
	}
}

func (l *Logger) log(level LogLevel, message string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("[%s] [%s] %s", timestamp, level, message)
}

func (l *Logger) Info(message string) {
	l.log(LogInfo, message)
}

func (l *Logger) Warn(message string) {
	l.log(LogWarn, message)
}

func (l *Logger) Error(message string) {
	l.log(LogError, message)
}

func (l *Logger) Debug(message string) {
	if l.verbose {
		l.log(LogInfo, "[DEBUG] "+message)
	}
}

func (l *Logger) Debugf(format string, args ...interface{}) {
	if l.verbose {
		l.log(LogInfo, fmt.Sprintf("[DEBUG] "+format, args...))
	}
}

func (l *Logger) Infof(format string, args ...interface{}) {
	l.Info(fmt.Sprintf(format, args...))
}

func (l *Logger) Warnf(format string, args ...interface{}) {
	l.Warn(fmt.Sprintf(format, args...))
}

func (l *Logger) Errorf(format string, args ...interface{}) {
	l.Error(fmt.Sprintf(format, args...))
}

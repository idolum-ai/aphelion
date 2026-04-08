//go:build linux && !go1.21

package core

import "log"

type routerLogger interface {
	Debug(msg string, args ...interface{})
	Error(msg string, args ...interface{})
}

type stdRouterLogger struct{}

func defaultRouterLogger() routerLogger {
	return stdRouterLogger{}
}

func (stdRouterLogger) Debug(msg string, args ...interface{}) {
	log.Printf("DEBUG: %s %v", msg, args)
}

func (stdRouterLogger) Error(msg string, args ...interface{}) {
	log.Printf("ERROR: %s %v", msg, args)
}

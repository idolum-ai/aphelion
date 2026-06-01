//go:build linux && go1.21

package router

import "log/slog"

type routerLogger interface {
	Debug(msg string, args ...interface{})
	Error(msg string, args ...interface{})
}

func defaultRouterLogger() routerLogger {
	return slog.Default()
}

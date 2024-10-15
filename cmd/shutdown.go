package cmd

import (
	"os"

	"github.com/flanksource/commons/logger"
)

var shutdownHooks []func()

func Shutdown() {
	if len(shutdownHooks) == 0 {
		return
	}

	logger.Infof("executing %d shutdown hooks", len(shutdownHooks))
	for _, fn := range shutdownHooks {
		fn()
	}
	shutdownHooks = []func(){}
}

func ShutdownAndExit(code int, msg string) {
	Shutdown()

	if code == 0 {
		logger.StandardLogger().WithSkipReportLevel(1).Infof(msg)
	} else {
		logger.StandardLogger().WithSkipReportLevel(1).Errorf(msg)
	}

	os.Exit(code)
}

func AddShutdownHook(fn func()) {
	shutdownHooks = append(shutdownHooks, fn)
}

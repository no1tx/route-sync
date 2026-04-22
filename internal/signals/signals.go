package signals

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

type Set struct {
	Context context.Context
	Stop    context.CancelFunc
	Reload  <-chan os.Signal
}

func Notify(parent context.Context) Set {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	reload := make(chan os.Signal, 1)
	signal.Notify(reload, syscall.SIGHUP)
	return Set{Context: ctx, Stop: stop, Reload: reload}
}

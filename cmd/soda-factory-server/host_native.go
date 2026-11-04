package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryruntime "github.com/levitateos/factory/pkg/factory/runtime"
	"github.com/levitateos/factory/pkg/store"
)

func run() error {
	flags := flag.NewFlagSet("soda-factory-server", flag.ContinueOnError)
	config := flags.String("config", "", "absolute owned native backend.json path")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *config == "" || flags.NArg() != 0 {
		return errors.New("native Factory requires --config with its owned bootstrap path")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return factoryruntime.RunNative(ctx, *config, func(profile collaboration.Profile, db *store.Store) http.Handler {
		return factoryruntime.NewNativeObservationHandler(profile, collaboration.GitHub, "https://api.github.com", db)
	})
}

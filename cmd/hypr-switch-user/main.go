package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/paulgmiller/hybuntu/internal/switchuser"
)

const programVersion = "0.1.0"

func main() {
	os.Exit(run())
}

func run() int {
	defaults := switchuser.DefaultConfig()
	flags := flag.NewFlagSet("hypr-switch-user", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	greeterVT := flags.Uint("greeter-vt", uint(defaults.GreeterVT), "GDM's configured initial virtual terminal")
	lockTimeout := flags.Duration("lock-timeout", defaults.LockTimeout, "maximum time to wait for session-lock confirmation")
	greeterTimeout := flags.Duration("greeter-timeout", defaults.GreeterTimeout, "maximum time to wait for an active GDM greeter")
	dbusTimeout := flags.Duration("dbus-timeout", 3*time.Second, "timeout for each D-Bus method/property call")
	verbose := flags.Bool("verbose", false, "enable diagnostic logging")
	showVersion := flags.Bool("version", false, "print version and exit")

	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: hypr-switch-user [options]\n\n")
		fmt.Fprintln(flags.Output(), "Securely lock the active Hyprland session and switch to GDM's greeter.")
		fmt.Fprintln(flags.Output(), "\nOptions:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "hypr-switch-user: unexpected arguments: %v\n", flags.Args())
		flags.Usage()
		return 2
	}
	if *showVersion {
		fmt.Printf("hypr-switch-user %s\n", programVersion)
		return 0
	}
	if *greeterVT > 63 {
		fmt.Fprintf(os.Stderr, "hypr-switch-user: --greeter-vt must be between 1 and 63\n")
		return 2
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	system, err := switchuser.ConnectSystemBus(ctx, *dbusTimeout)
	if err != nil {
		logger.Error("cannot initialize", "error", err)
		return 1
	}
	defer func() {
		if err := system.Close(); err != nil {
			logger.Debug("close system bus", "error", err)
		}
	}()

	config := defaults
	config.GreeterVT = uint32(*greeterVT)
	config.LockTimeout = *lockTimeout
	config.GreeterTimeout = *greeterTimeout

	switcher, err := switchuser.New(system, config, logger)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		return 2
	}
	if err := switcher.Run(ctx, uint32(os.Getpid())); err != nil {
		logger.Error("switch user failed", "error", err)
		return 1
	}
	return 0
}

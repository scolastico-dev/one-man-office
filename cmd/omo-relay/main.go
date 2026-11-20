// Command omo-relay runs the standalone, stateless WebSocket relay.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/relayserver"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "omo-relay:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("omo-relay", flag.ContinueOnError)
	flags.SetOutput(out)
	listen := flags.String("listen", ":8091", "HTTP listen address (put HTTPS/WSS in front for remote use)")
	password := flags.String("password-file", "", "optional admission password file or Docker secret")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *showVersion {
		_, err := fmt.Fprintln(out, "omo-relay", version)
		return err
	}
	return serve(ctx, *listen, *password, func(addr string) { fmt.Fprintln(out, "omo-relay listening on", addr) })
}

func serve(ctx context.Context, listen, passwordFile string, ready func(string)) error {
	relay, err := relayserver.New(relayserver.Config{ListenAddr: listen, PasswordFile: passwordFile})
	if err != nil {
		return err
	}
	defer relay.Close()
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	srv := &http.Server{Handler: relay, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	ready(ln.Addr().String())
	select {
	case err = <-done:
	case <-ctx.Done():
		// Shutdown does not own hijacked sockets; close those explicitly first.
		relay.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = srv.Shutdown(shutdownCtx)
		cancel()
		if err != nil {
			_ = srv.Close()
		}
		serveErr := <-done
		if err == nil {
			err = serveErr
		}
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

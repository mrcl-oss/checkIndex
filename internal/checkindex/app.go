package checkindex

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// RunCLI runs checkindex with the supplied arguments and output writer.
func RunCLI(args []string, out io.Writer) error {
	report := func(_ context.Context, p Profile, o Options, out io.Writer) error {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return collect(ctx, p, o, out, dialMongo)
	}
	err := newCommand("", out, io.Discard, report).Run(context.Background(), append([]string{"checkindex"}, args...))
	if err != nil && hasHelpFlag(args) {
		return nil
	}
	return err
}

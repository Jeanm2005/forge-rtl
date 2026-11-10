package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	pb "github.com/Jeanm2005/forge-rtl/controlplane/api/v1"
	"github.com/Jeanm2005/forge-rtl/controlplane/internal/control"
	"google.golang.org/grpc"
)

func run() error {
	root := flag.String("root", ".", "trusted repository root")
	state := flag.String("state", "build/control-state", "durable operator-owned state directory")
	worker := flag.String("worker", "worker/target/debug/forge-worker", "trusted Rust worker executable")
	toolchain := flag.String("toolchain", ".tools/oss-cad-suite", "trusted EDA suite root")
	flag.Parse()
	var err error
	for _, path := range []*string{root, state, worker, toolchain} {
		*path, err = filepath.Abs(*path)
		if err != nil {
			return err
		}
	}
	store, err := control.OpenStore(*state, nil)
	if err != nil {
		return err
	}
	defer store.Close()
	validator, err := control.NewValidator(*root, *worker, *toolchain)
	if err != nil {
		return err
	}
	runner := &control.Worker{Root: *root, Binary: *worker, Toolchain: *toolchain, Runs: filepath.Join(*state, "runs")}
	engine := control.NewEngine(store, runner, validator)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	recoverCtx, recoverCancel := context.WithTimeout(ctx, 30*time.Second)
	defer recoverCancel()
	if err = engine.Recover(recoverCtx); err != nil {
		return err
	}
	socket := filepath.Join(*state, "control.sock")
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refusing to replace nonsocket %s", socket)
		}
		if err = os.Remove(socket); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err = os.Chmod(socket, 0600); err != nil {
		return err
	}
	server := grpc.NewServer(grpc.MaxRecvMsgSize(2*1024*1024), grpc.MaxSendMsgSize(10*1024*1024), grpc.MaxConcurrentStreams(32))
	pb.RegisterControlServer(server, &control.Server{Engine: engine})
	results := make(chan error, 2)
	go func() { results <- engine.Run(ctx) }()
	go func() { results <- server.Serve(listener) }()
	fmt.Println(socket)
	received := 0
	select {
	case <-ctx.Done():
	case err = <-results:
		received = 1
	}
	cancel()
	server.Stop()
	// Run waits for worker service cleanup before releasing the durable-state lock.
	for count := received; count < 2; count++ {
		select {
		case result := <-results:
			if err == nil {
				err = result
			}
		case <-time.After(45 * time.Second):
			return fmt.Errorf("shutdown cleanup deadline exceeded")
		}
	}
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	pb "github.com/Jeanm2005/forge-rtl/controlplane/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func run() error {
	socket := flag.String("socket", "build/control-state/control.sock", "operator's Unix socket")
	id := flag.String("id", "", "job identity")
	attempt := flag.String("attempt", "", "attempt identity (latest by default)")
	key := flag.String("key", "", "submission idempotency key")
	source := flag.String("candidate", "rtl/candidate/mac_candidate.sv", "replacement source file")
	timeout := flag.Uint("timeout", 1800, "overall job deadline in seconds")
	budget := flag.Uint("max-attempts", 2, "maximum execution attempts")
	flag.Parse()
	if flag.NArg() != 1 {
		return errors.New("usage: forge-client [flags] submit|get|cancel|evidence|review")
	}
	path, err := filepath.Abs(*socket)
	if err != nil {
		return err
	}
	connection, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(10*1024*1024)))
	if err != nil {
		return err
	}
	defer connection.Close()
	client := pb.NewControlClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result proto.Message
	switch flag.Arg(0) {
	case "submit":
		if *timeout > 3600 || *budget > 3 {
			return errors.New("timeout must be at most 3600 seconds and max-attempts at most 3")
		}
		file, err := os.Open(*source)
		if err != nil {
			return err
		}
		defer file.Close()
		bytes, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
		if err != nil {
			return err
		}
		if len(bytes) > 1024*1024 {
			return errors.New("candidate exceeds 1 MiB")
		}
		result, err = client.Submit(ctx, &pb.SubmitRequest{IdempotencyKey: *key, CandidatePath: "rtl/candidate/mac_candidate.sv", CandidateSource: bytes, TimeoutSeconds: uint32(*timeout), MaxAttempts: uint32(*budget)})
		if err != nil {
			return err
		}
	case "get":
		result, err = client.Get(ctx, &pb.GetRequest{JobId: *id})
	case "cancel":
		result, err = client.Cancel(ctx, &pb.GetRequest{JobId: *id})
	case "review":
		result, err = client.QueueReview(ctx, &pb.GetRequest{JobId: *id})
	case "evidence":
		evidence, err := client.GetEvidence(ctx, &pb.EvidenceRequest{JobId: *id, AttemptId: *attempt})
		if err != nil {
			return err
		}
		hash := sha256.Sum256(evidence.ManifestJson)
		if hex.EncodeToString(hash[:]) != evidence.ManifestSha256 {
			return errors.New("RPC evidence hash mismatch")
		}
		_, err = os.Stdout.Write(append(evidence.ManifestJson, '\n'))
		return err
	default:
		return errors.New("unknown command")
	}
	if err != nil {
		return err
	}
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true, Indent: "  "}).Marshal(result)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

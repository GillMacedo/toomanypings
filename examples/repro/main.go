// Command repro runs a real grpc-go server and client in one process to
// show when keepalive pings end in GOAWAY ENHANCE_YOUR_CALM "too_many_pings".
// The server's handler stays silent for -rpc, so the client's keepalive is
// the only traffic. With -rpc 0 there is no RPC, and the client's
// connection state is printed for -watch. Run it from examples/ with
// go run ./repro; it needs network access to download grpc-go.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/emptypb"
)

func main() {
	clientTime := flag.Duration("client-time", 10*time.Second, "")
	clientPWS := flag.Bool("client-pws", false, "")
	serverMin := flag.Duration("server-min-time", 0, "0 = no EnforcementPolicy option")
	serverPWS := flag.Bool("server-pws", false, "")
	serverTime := flag.Duration("server-time", 0, "0 = no ServerParameters option")
	rpc := flag.Duration("rpc", 60*time.Second, "how long the RPC handler stays silent; 0 = no RPC, idle connection")
	watch := flag.Duration("watch", 60*time.Second, "")
	flag.Parse()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	var sopts []grpc.ServerOption
	if *serverMin > 0 || *serverPWS {
		sopts = append(sopts, grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: *serverMin, PermitWithoutStream: *serverPWS}))
	}
	if *serverTime > 0 {
		sopts = append(sopts, grpc.KeepaliveParams(keepalive.ServerParameters{Time: *serverTime}))
	}
	sopts = append(sopts, grpc.UnknownServiceHandler(func(srv any, stream grpc.ServerStream) error {
		var in emptypb.Empty
		if err := stream.RecvMsg(&in); err != nil {
			return err
		}
		time.Sleep(*rpc)
		return stream.SendMsg(&emptypb.Empty{})
	}))
	s := grpc.NewServer(sopts...)
	go s.Serve(lis)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: *clientTime, Timeout: 5 * time.Second, PermitWithoutStream: *clientPWS}))
	if err != nil {
		panic(err)
	}
	start := time.Now()
	if *rpc > 0 {
		err := conn.Invoke(context.Background(), "/demo.Slow/Wait", &emptypb.Empty{}, &emptypb.Empty{})
		fmt.Printf("rpc after %s: err=%v\n", time.Since(start).Round(time.Second), err)
		return
	}
	conn.Connect()
	state := conn.GetState()
	deadline := time.Now().Add(*watch)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		if !conn.WaitForStateChange(ctx, state) {
			cancel()
			break
		}
		cancel()
		state = conn.GetState()
		fmt.Printf("%s: state %s\n", time.Since(start).Round(time.Second), state)
		if state == connectivity.Idle {
			conn.Connect()
		}
	}
	os.Exit(0)
}

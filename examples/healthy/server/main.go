// Command server runs an inventory service. Its enforcement policy allows
// the watch client's 15s pings, also on connections with no RPC running.
package main

import (
	"log"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

func main() {
	lis, err := net.Listen("tcp", ":7070")
	if err != nil {
		log.Fatal(err)
	}
	srv := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 15 * time.Minute,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	log.Fatal(srv.Serve(lis))
}

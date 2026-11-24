// Command server runs an inventory service. It sets an idle limit for
// connections but no keepalive.EnforcementPolicy, so grpc-go enforces the
// default: one client ping every 5 minutes at most.
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
	)
	log.Fatal(srv.Serve(lis))
}

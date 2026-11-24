# Roadmap

## Next
- Read keepalive values from flag defaults (`flag.Duration`), and say they can be overridden
- `--client` and `--server` to say which client dials which server, so a KA103 note can become a direct answer
- etcd's `clientv3.Config{DialKeepAliveTime}` and `--grpc-keepalive-min-time`, the most common wrapper around grpc-go keepalive

## Later
- grpc-java (`keepAliveTime`, `permitKeepAliveTime`) and Python or C++ channel arguments
- Envoy and service mesh settings, since pings stop at the proxy
- Read a too_many_pings log line and point to the client setting that caused it

# envoy-control-plane
Experimental control plane server for envoy


## Steps

#### Go to cmd/microgateway

Edit the following array with actual endpoints. also the port
``` slicr := []string{"host.docker.internal", "host.docker.internal", "host.docker.internal"} ```

```
        h := &core.Address{Address: &core.Address_SocketAddress{
                SocketAddress: &core.SocketAddress{
                    Address:  remoteHost,
                    Protocol: core.SocketAddress_TCP,
                    PortSpecifier: &core.SocketAddress_PortValue{
                        PortValue: uint32(3001),
                    },
                },
            }}`
```

1. go mod vendor
2. go run main.go

#### Start envoy

```docker-compose up```
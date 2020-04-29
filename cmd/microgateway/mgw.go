package microgateway

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	"sync"

	v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/api/v2/endpoint"
	listener "github.com/envoyproxy/go-control-plane/envoy/api/v2/listener"
	v2route "github.com/envoyproxy/go-control-plane/envoy/api/v2/route"
	hcm "github.com/envoyproxy/go-control-plane/envoy/config/filter/network/http_connection_manager/v2"
	"github.com/golang/protobuf/ptypes"
	"github.com/wso2/envoy-control-plane/internal/pkg/accesslogs"
	myals "github.com/wso2/envoy-control-plane/internal/pkg/accesslogs"

	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cache "github.com/envoyproxy/go-control-plane/pkg/cache/v2"
	xds "github.com/envoyproxy/go-control-plane/pkg/server/v2"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"

	accesslog "github.com/envoyproxy/go-control-plane/envoy/service/accesslog/v2"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v2"
)

var (
	debug       bool
	onlyLogging bool

	localhost = "0.0.0.0"

	port        uint
	gatewayPort uint
	alsPort     uint

	mode string

	version int32

	config cache.SnapshotCache

	strSlice = []string{"www.bbc.com", "www.yahoo.com", "blog.salrashid.me"}
)

const (
	XdsCluster = "xds_cluster"
	Ads        = "ads"
	Xds        = "xds"
	Rest       = "rest"
)

func init() {
	flag.BoolVar(&debug, "debug", true, "Use debug logging")
	flag.BoolVar(&onlyLogging, "onlyLogging", false, "Only demo AccessLogging Service")
	flag.UintVar(&port, "port", 18000, "Management server port")
	flag.UintVar(&gatewayPort, "gateway", 18001, "Management server port for HTTP gateway")
	flag.UintVar(&alsPort, "als", 18090, "Accesslog server port")
	flag.StringVar(&mode, "ads", Ads, "Management server type (ads, xds, rest)")
}

type logger struct{}

func (logger logger) Infof(format string, args ...interface{}) {
	log.Infof(format, args...)
}
func (logger logger) Errorf(format string, args ...interface{}) {
	log.Errorf(format, args...)
}
func (cb *callbacks) Report() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	log.WithFields(log.Fields{"fetches": cb.fetches, "requests": cb.requests}).Info("cb.Report()  callbacks")
}
func (cb *callbacks) OnStreamOpen(ctx context.Context, id int64, typ string) error {
	log.Infof("OnStreamOpen %d open for %s", id, typ)
	return nil
}
func (cb *callbacks) OnStreamClosed(id int64) {
	log.Infof("OnStreamClosed %d closed", id)
}
func (cb *callbacks) OnStreamRequest(int64, *v2.DiscoveryRequest) error {
	log.Infof("OnStreamRequest")
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.requests++
	if cb.signal != nil {
		close(cb.signal)
		cb.signal = nil
	}
	return nil
}
func (cb *callbacks) OnStreamResponse(int64, *v2.DiscoveryRequest, *v2.DiscoveryResponse) {
	log.Infof("OnStreamResponse...")
	cb.Report()
}
func (cb *callbacks) OnFetchRequest(ctx context.Context, req *v2.DiscoveryRequest) error {
	log.Infof("OnFetchRequest...")
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.fetches++
	if cb.signal != nil {
		close(cb.signal)
		cb.signal = nil
	}
	return nil
}
func (cb *callbacks) OnFetchResponse(*v2.DiscoveryRequest, *v2.DiscoveryResponse) {}

type callbacks struct {
	signal   chan struct{}
	fetches  int
	requests int
	mu       sync.Mutex
}

// Hasher returns node ID as an ID
type Hasher struct {
}

// ID function
func (h Hasher) ID(node *core.Node) string {
	if node == nil {
		return "unknown"
	}
	return node.Id
}

//RunAccessLogServer starts an accesslog service.
func RunAccessLogServer(ctx context.Context, als *myals.AccessLogService, port uint) {
	grpcServer := grpc.NewServer()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.WithError(err).Fatal("failed to listen")
	}

	accesslog.RegisterAccessLogServiceServer(grpcServer, als)
	log.WithFields(log.Fields{"port": port}).Info("access log server listening")
	//log.Fatalf("", Serve(lis))
	// go func() {
	if err = grpcServer.Serve(lis); err != nil {
		log.Error(err)
	}
	// }()
	// <-ctx.Done()

	//grpcServer.GracefulStop()
}

const grpcMaxConcurrentStreams = 1000000

// RunManagementServer starts an xDS server at the given port.
func RunManagementServer(ctx context.Context, server xds.Server, port uint) {
	var grpcOptions []grpc.ServerOption
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(grpcMaxConcurrentStreams))
	grpcServer := grpc.NewServer(grpcOptions...)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.WithError(err).Fatal("failed to listen")
	}

	// register services
	discovery.RegisterAggregatedDiscoveryServiceServer(grpcServer, server)
	v2.RegisterEndpointDiscoveryServiceServer(grpcServer, server)
	v2.RegisterClusterDiscoveryServiceServer(grpcServer, server)
	v2.RegisterRouteDiscoveryServiceServer(grpcServer, server)
	v2.RegisterListenerDiscoveryServiceServer(grpcServer, server)

	log.WithFields(log.Fields{"port": port}).Info("management server listening")
	//log.Fatalf("", Serve(lis))
	// go func() {
	if err = grpcServer.Serve(lis); err != nil {
		log.Error(err)
	}
	// }()
	//<-ctx.Done()

	// grpcServer.GracefulStop()
}

//RunManagementGateway starts an HTTP gateway to an xDS server.
func RunManagementGateway(ctx context.Context, srv xds.Server, port uint) {
	log.WithFields(log.Fields{"port": port}).Info("gateway listening HTTP/1.1")
	server := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: HTTPGateway{Server: srv}}
	go func() {
		if err := server.ListenAndServe(); err != nil {
			log.Error(err)
		}
	}()
}

// Run ...
func Run() {
	sig := make(chan os.Signal)
	signal.Notify(sig, os.Interrupt)

	flag.Parse()
	if debug {
		log.SetLevel(log.DebugLevel)
	}
	ctx := context.Background()

	log.Printf("Starting control plane")

	signal := make(chan struct{})
	cb := &callbacks{
		signal:   signal,
		fetches:  0,
		requests: 0,
	}
	config = cache.NewSnapshotCache(mode == Ads, Hasher{}, nil)

	srv := xds.NewServer(ctx, config, cb)

	als := &accesslogs.AccessLogService{}
	als = &myals.AccessLogService{}
	go RunAccessLogServer(ctx, als, alsPort)

	if onlyLogging {
		cc := make(chan struct{})
		<-cc
		os.Exit(0)
	}

	// start the xDS server
	go RunManagementServer(ctx, srv, port)
	go RunManagementGateway(ctx, srv, gatewayPort)

	<-signal

	als.Dump(func(s string) { log.Debug(s) })
	cb.Report()

	//for {

	slicr := []string{"host.docker.internal", "host.docker.internal", "host.docker.internal"}

	for _, v := range slicr {

		nodeId := config.GetStatusKeys()[0]

		var clusterName = "service_bbc"
		var remoteHost = v
		// var sni = v
		log.Infof(">>>>>>>>>>>>>>>>>>> creating cluster %v  with  remoteHost %v", clusterName, v)

		//c := []cache.Resource{resource.MakeCluster(resource.Ads, clusterName)}

		h := &core.Address{Address: &core.Address_SocketAddress{
			SocketAddress: &core.SocketAddress{
				Address:  remoteHost,
				Protocol: core.SocketAddress_TCP,
				PortSpecifier: &core.SocketAddress_PortValue{
					PortValue: uint32(3001),
				},
			},
		}}

		// 	- name: ext-authz
		// type: STRICT_DNS
		// http2_protocol_options: {}
		// load_assignment:
		//   cluster_name: ext-authz
		//   endpoints:
		//   - lb_endpoints:
		//     - endpoint:
		//         address:
		//           socket_address:
		//             address: host.docker.internal
		//             port_value: 8081

		cluster := &v2.Cluster{
			Name:                 "ext-authz",
			ConnectTimeout:       &durationpb.Duration{Seconds: 2},
			DnsLookupFamily:      v2.Cluster_V4_ONLY,
			ClusterDiscoveryType: &v2.Cluster_Type{v2.Cluster_LOGICAL_DNS},
			LoadAssignment: &v2.ClusterLoadAssignment{
				ClusterName: "ext-authz",
				Endpoints: []*endpoint.LocalityLbEndpoints{
					&endpoint.LocalityLbEndpoints{
						LbEndpoints: []*endpoint.LbEndpoint{
							&endpoint.LbEndpoint{
								HostIdentifier: &endpoint.LbEndpoint_Endpoint{
									Endpoint: &endpoint.Endpoint{
										Address: &core.Address{
											Address: &core.Address_SocketAddress{
												SocketAddress: &core.SocketAddress{
													Address: "host.docker.internal",
													PortSpecifier: &core.SocketAddress_PortValue{
														PortValue: 8008,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		c := []types.Resource{
			&v2.Cluster{
				Name:                 clusterName,
				ConnectTimeout:       ptypes.DurationProto(2 * time.Second),
				ClusterDiscoveryType: &v2.Cluster_Type{Type: v2.Cluster_LOGICAL_DNS},
				DnsLookupFamily:      v2.Cluster_V4_ONLY,
				LbPolicy:             v2.Cluster_ROUND_ROBIN,
				Hosts:                []*core.Address{h},
			},
			cluster,
		}

		// =================================================================================
		var listenerName = "listener_0"
		var targetHost = v
		var targetRegex = "/"
		var virtualHostName = "local_service"
		var routeConfigName = "local_route"

		log.Infof(">>>>>>>>>>>>>>>>>>> creating listener " + listenerName)

		v := v2route.VirtualHost{
			Name:    virtualHostName,
			Domains: []string{"*"},
			Routes: []*v2route.Route{
				&v2route.Route{
					Match: &v2route.RouteMatch{
						PathSpecifier: &v2route.RouteMatch_Prefix{
							Prefix: targetRegex,
						},
					},
					Action: &v2route.Route_Route{
						Route: &v2route.RouteAction{
							HostRewriteSpecifier: &v2route.RouteAction_HostRewrite{
								HostRewrite: targetHost,
							},
							ClusterSpecifier: &v2route.RouteAction_Cluster{
								Cluster: clusterName,
							},
							PrefixRewrite: "/pet",
						},
					},
				},
			},
		}

		// extAuthzConfig :=

		manager := &hcm.HttpConnectionManager{
			CodecType:  hcm.HttpConnectionManager_AUTO,
			StatPrefix: "ingress_http",
			RouteSpecifier: &hcm.HttpConnectionManager_RouteConfig{
				RouteConfig: &v2.RouteConfiguration{
					Name:         routeConfigName,
					VirtualHosts: []*v2route.VirtualHost{&v},
				},
			},
			HttpFilters: []*hcm.HttpFilter{
				&hcm.HttpFilter{
					Name: wellknown.Router,
				},
			},
		}

		pbst, err := ptypes.MarshalAny(manager)
		if err != nil {
			panic(err)
		}

		var l = []types.Resource{
			&v2.Listener{
				Name: listenerName,
				Address: &core.Address{
					Address: &core.Address_SocketAddress{
						SocketAddress: &core.SocketAddress{
							Protocol: core.SocketAddress_TCP,
							Address:  localhost,
							PortSpecifier: &core.SocketAddress_PortValue{
								PortValue: 10000,
							},
						},
					},
				},
				FilterChains: []*listener.FilterChain{{
					Filters: []*listener.Filter{{
						Name: wellknown.HTTPConnectionManager,
						ConfigType: &listener.Filter_TypedConfig{
							TypedConfig: pbst,
						},
					}},
				}},
			}}

		// =================================================================================

		atomic.AddInt32(&version, 1)
		log.Infof(">>>>>>>>>>>>>>>>>>> creating snapshot Version " + fmt.Sprint(version))
		snap := cache.NewSnapshot(fmt.Sprint(version), nil, c, nil, l, nil)

		config.SetSnapshot(nodeId, snap)

		//reader := bufio.NewReader(os.Stdin)
		//_, _ = reader.ReadString('\n')

		time.Sleep(2 * time.Second)

	}

	<-sig

}

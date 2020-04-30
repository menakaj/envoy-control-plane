package microgateway

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	"sync"

	v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	"github.com/wso2/envoy-control-plane/internal/pkg/accesslogs"
	myals "github.com/wso2/envoy-control-plane/internal/pkg/accesslogs"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v2"
	xds "github.com/envoyproxy/go-control-plane/pkg/server/v2"
	oasParser "github.com/wso2/envoy-control-plane/internal/pkg/configurator"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"

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
	log.Infof("OnFetchRequest...", req)
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.fetches++
	if cb.signal != nil {
		close(cb.signal)
		cb.signal = nil
	}
	return nil
}
func (cb *callbacks) OnFetchResponse(req *v2.DiscoveryRequest, res *v2.DiscoveryResponse) {
	fmt.Println(req)
	fmt.Println(res)
}

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
	go func() {
		if err = grpcServer.Serve(lis); err != nil {
			log.Error(err)
		}
	}()
	<-ctx.Done()

	grpcServer.GracefulStop()
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
	go func() {
		go func() {
			if err = grpcServer.Serve(lis); err != nil {
				log.Error(err)
			}
		}()
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()
}

// //RunManagementGateway starts an HTTP gateway to an xDS server.
// func RunManagementGateway(ctx context.Context, srv xds.Server, port uint) {
// 	log.WithFields(log.Fields{"port": port}).Info("gateway listening HTTP/1.1")
// 	server := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: HTTPGateway{Server: srv}}
// 	go func() {
// 		if err := server.ListenAndServe(); err != nil {
// 			log.Error(err)
// 		}
// 	}()
// }

// Run ...
func Run() {
	sig := make(chan os.Signal)
	signal.Notify(sig, os.Interrupt)

	fmt.Println(os.Args)

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
	// go RunManagementGateway(ctx, srv, gatewayPort)

	<-signal

	als.Dump(func(s string) { log.Debug(s) })
	cb.Report()

	//for {

	slicr := []string{"host.docker.internal", "host.docker.internal", "host.docker.internal"}

	for _, v := range slicr {

		var nodeId string
		if len(config.GetStatusKeys()) > 0 {
			nodeId = config.GetStatusKeys()[0]
		}

		var clusterName = "service_bbc"
		var remoteHost = v
		// var sni = v
		log.Infof(">>>>>>>>>>>>>>>>>>> creating cluster %v  with  remoteHost %c", clusterName, v)

		//c := []cache.Resource{resource.MakeCluster(resource.Ads, clusterName)}

		_, c, l, _ := oasParser.GetConfigs(remoteHost, clusterName)

		//log.Println(e, r, c, l)

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

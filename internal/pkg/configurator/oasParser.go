package configurator

import (
	"fmt"
	"github.com/envoyproxy/go-control-plane/envoy/api/v2/endpoint"
	"google.golang.org/protobuf/types/known/durationpb"
	"time"

	v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	listener "github.com/envoyproxy/go-control-plane/envoy/api/v2/listener"
	v2route "github.com/envoyproxy/go-control-plane/envoy/api/v2/route"
	envoy_config_filter_http_ext_authz_v2 "github.com/envoyproxy/go-control-plane/envoy/config/filter/http/ext_authz/v2"
	hcm "github.com/envoyproxy/go-control-plane/envoy/config/filter/network/http_connection_manager/v2"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/golang/protobuf/ptypes"
	"github.com/sirupsen/logrus"
)

type oastoyaml interface {
	ParseClusters() []types.Resource
	ParseListeners() []types.Resource
	ParseEndpoints() []types.Resource
}

type OASParser struct{}

func (p *OASParser) ParseClusters() []types.Resource {
	return nil
}

// This reader reads swagger yaml from the input location and generates the required envoy resources.
func GetConfigs(remoteHost string, clusterName string) ([]types.Resource, []types.Resource, []types.Resource, []types.Resource) {

	h := &core.Address{Address: &core.Address_SocketAddress{
		SocketAddress: &core.SocketAddress{
			Address:  remoteHost,
			Protocol: core.SocketAddress_TCP,
			PortSpecifier: &core.SocketAddress_PortValue{
				PortValue: uint32(3001),
			},
		},
	}}

	h2 := &core.Address{Address: &core.Address_SocketAddress{
		SocketAddress: &core.SocketAddress{
			Address: remoteHost,
			PortSpecifier: &core.SocketAddress_PortValue{
				PortValue: uint32(8008),
			},
		},
	}}

	c := []types.Resource{
		&v2.Cluster{
			Name:                 clusterName,
			ConnectTimeout:       ptypes.DurationProto(2 * time.Second),
			ClusterDiscoveryType: &v2.Cluster_Type{Type: v2.Cluster_LOGICAL_DNS},
			DnsLookupFamily:      v2.Cluster_V4_ONLY,
			LbPolicy:             v2.Cluster_ROUND_ROBIN,
			LoadAssignment: &v2.ClusterLoadAssignment{
				ClusterName: clusterName,
				Endpoints: []*envoy_api_v2_endpoint.LocalityLbEndpoints{
					{
						LbEndpoints: []*envoy_api_v2_endpoint.LbEndpoint{
							{
								HostIdentifier: &envoy_api_v2_endpoint.LbEndpoint_Endpoint{
									Endpoint: &envoy_api_v2_endpoint.Endpoint{
										Address: h,
									},
								},
							},
						},
					},
				},
			},
		},
		&v2.Cluster{
			Name:                 "ext-authz",
			ConnectTimeout:       ptypes.DurationProto(10 * time.Second),
			ClusterDiscoveryType: &v2.Cluster_Type{Type: v2.Cluster_STRICT_DNS},
			Http2ProtocolOptions: &core.Http2ProtocolOptions{},
			DnsLookupFamily:      v2.Cluster_V4_ONLY,
			LoadAssignment: &v2.ClusterLoadAssignment{
				ClusterName: clusterName,
				Endpoints: []*envoy_api_v2_endpoint.LocalityLbEndpoints{
					{
						LbEndpoints: []*envoy_api_v2_endpoint.LbEndpoint{
							{
								HostIdentifier: &envoy_api_v2_endpoint.LbEndpoint_Endpoint{
									Endpoint: &envoy_api_v2_endpoint.Endpoint{
										Address: h2,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// =================================================================================
	var listenerName = "listener_0"
	var targetHost = "host.docker.internal"
	var targetRegex = "/"
	var virtualHostName = "local_service"
	var routeConfigName = "local_route"

	//fmt.Println(c)

	logrus.Infof(">>>>>>>>>>>>>>>>>>> creating listener " + listenerName)

	v := v2route.VirtualHost{
		Name:    virtualHostName,
		Domains: []string{"*"},
		Routes: []*v2route.Route{
			{
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

	extAuthzConfig := &envoy_config_filter_http_ext_authz_v2.ExtAuthz{
		Services: &envoy_config_filter_http_ext_authz_v2.ExtAuthz_GrpcService{
			GrpcService: &core.GrpcService{
				TargetSpecifier: &core.GrpcService_EnvoyGrpc_{
					EnvoyGrpc: &core.GrpcService_EnvoyGrpc{
						ClusterName: "ext-authz",
					},
				},
				Timeout: &durationpb.Duration{Seconds: int64(100)},
			},
		},
		WithRequestBody: &envoy_config_filter_http_ext_authz_v2.BufferSettings{
			MaxRequestBytes:     1000,
			AllowPartialMessage: false,
		},
		MetadataContextNamespaces: []string{"sadasd", "asdasdsd"},
	}

	ext, err2 := ptypes.MarshalAny(extAuthzConfig)
	if err2 != nil {
		panic(err2)
	}
	fmt.Println(ext)

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
			{
				Name: "envoy.filters.http.ext_authz",
				ConfigType: &hcm.HttpFilter_TypedConfig{
					TypedConfig: ext,
				},
			},
			{
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
						Address:  "0.0.0.0",
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

	return nil, c, l, nil
}

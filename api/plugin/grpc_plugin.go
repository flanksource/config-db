package plugin

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pb "github.com/flanksource/config-db/api/plugin/proto"
)

type ScraperPlugin interface {
	Scrape(ctx context.Context, req *pb.ScrapeRequest) (*pb.ScrapeResponse, error)
	CanScrape(ctx context.Context, specJSON []byte) (bool, error)
	GetInfo(ctx context.Context) (*PluginInfo, error)
}

type GRPCPlugin struct {
	plugin.Plugin
	Impl         ScraperPlugin
	HostServices HostServices
}

func (p *GRPCPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterScraperPluginServer(s, &grpcServer{impl: p.Impl})
	return nil
}

func (p *GRPCPlugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	hostConn, err := broker.Dial(1)
	if err != nil {
		return nil, err
	}
	return &grpcClient{
		client:     pb.NewScraperPluginClient(c),
		hostClient: pb.NewHostServicesClient(hostConn),
	}, nil
}

type grpcServer struct {
	pb.UnimplementedScraperPluginServer
	impl ScraperPlugin
}

func (s *grpcServer) Scrape(ctx context.Context, req *pb.ScrapeRequest) (*pb.ScrapeResponse, error) {
	return s.impl.Scrape(ctx, req)
}

func (s *grpcServer) CanScrape(ctx context.Context, req *pb.CanScrapeRequest) (*pb.CanScrapeResponse, error) {
	can, err := s.impl.CanScrape(ctx, req.SpecJson)
	if err != nil {
		return &pb.CanScrapeResponse{CanScrape: false}, err
	}
	return &pb.CanScrapeResponse{CanScrape: can}, nil
}

func (s *grpcServer) GetInfo(ctx context.Context, _ *pb.Empty) (*pb.PluginInfo, error) {
	info, err := s.impl.GetInfo(ctx)
	if err != nil {
		return nil, err
	}
	return &pb.PluginInfo{
		Name:           info.Name,
		Version:        info.Version,
		SupportedTypes: info.SupportedTypes,
	}, nil
}

type grpcClient struct {
	client     pb.ScraperPluginClient
	hostClient pb.HostServicesClient
}

func (c *grpcClient) Scrape(ctx context.Context, req *pb.ScrapeRequest) (*pb.ScrapeResponse, error) {
	return c.client.Scrape(ctx, req)
}

func (c *grpcClient) CanScrape(ctx context.Context, specJSON []byte) (bool, error) {
	resp, err := c.client.CanScrape(ctx, &pb.CanScrapeRequest{SpecJson: specJSON})
	if err != nil {
		return false, err
	}
	return resp.CanScrape, nil
}

func (c *grpcClient) GetInfo(ctx context.Context) (*PluginInfo, error) {
	resp, err := c.client.GetInfo(ctx, &pb.Empty{})
	if err != nil {
		return nil, err
	}
	return &PluginInfo{
		Name:           resp.Name,
		Version:        resp.Version,
		SupportedTypes: resp.SupportedTypes,
	}, nil
}

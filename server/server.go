package server

import (
	"context"
	"errors"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mickamy/sql-tap/broker"
	"github.com/mickamy/sql-tap/explain"
	tapv1 "github.com/mickamy/sql-tap/gen/tap/v1"
	"github.com/mickamy/sql-tap/proxy"
)

// Server exposes a gRPC TapService for TUI clients to connect to.
type Server struct {
	grpcServer *grpc.Server
}

// New creates a new Server backed by the given Broker.
// explainClient may be nil if EXPLAIN is not configured.
func New(b *broker.Broker, explainClient *explain.Client) *Server {
	gs := grpc.NewServer()
	svc := &tapService{broker: b, explainClient: explainClient}
	tapv1.RegisterTapServiceServer(gs, svc)

	return &Server{grpcServer: gs}
}

// Serve starts the gRPC server on the given listener.
func (s *Server) Serve(lis net.Listener) error {
	if err := s.grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("server: serve: %w", err)
	}
	return nil
}

// Stop immediately stops the server, closing all active connections.
func (s *Server) Stop() {
	s.grpcServer.Stop()
}

// GracefulStop gracefully stops the server.
func (s *Server) GracefulStop() {
	s.grpcServer.GracefulStop()
}

type tapService struct {
	tapv1.UnimplementedTapServiceServer

	broker        *broker.Broker
	explainClient *explain.Client
}

func (s *tapService) Watch(_ *tapv1.WatchRequest, stream grpc.ServerStreamingServer[tapv1.WatchResponse]) error {
	ch, unsub := s.broker.Subscribe()
	defer unsub()

	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("server: watch: %w", ctx.Err())
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&tapv1.WatchResponse{
				Event: eventToProto(ev),
			}); err != nil {
				return fmt.Errorf("server: watch send: %w", err)
			}
		}
	}
}

func (s *tapService) Explain(ctx context.Context, req *tapv1.ExplainRequest) (*tapv1.ExplainResponse, error) {
	if s.explainClient == nil {
		return nil, status.Error(codes.FailedPrecondition, "EXPLAIN is not configured (set DATABASE_URL)")
	}

	mode := explain.Explain
	if req.GetAnalyze() {
		mode = explain.Analyze
	}

	result, err := s.explainClient.Run(ctx, mode, req.GetQuery(), req.GetArgs())
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, status.Error(codes.Canceled, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "explain: %v", err)
	}

	return &tapv1.ExplainResponse{Plan: result.Plan}, nil
}

func eventToProto(ev proxy.Event) *tapv1.QueryEvent {
	return &tapv1.QueryEvent{
		Id:           ev.ID,
		Op:           int32(ev.Op),
		Query:        ev.Query,
		Args:         ev.Args,
		StartTime:    timestamppb.New(ev.StartTime),
		Duration:     durationpb.New(ev.Duration),
		RowsAffected: ev.RowsAffected,
		Error:        ev.Error,
		TxId:         ev.TxID,
	}
}

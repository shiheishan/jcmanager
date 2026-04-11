package main

import (
	"context"
	"errors"
	"io"
	"testing"

	agentcfg "jcmanager/internal/agent"
	jcmanagerpb "jcmanager/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestHeartbeatLoopReturnsReRegisterOnUnauthenticated(t *testing.T) {
	client := &fakeAgentServiceClient{
		heartbeatErr: status.Error(codes.Unauthenticated, "stale session"),
	}

	err := heartbeatLoop(context.Background(), client, &testRuntimeConfig, agentState{
		NodeID:       "node-1",
		SessionToken: "stale-token",
	})
	if !errors.Is(err, errReRegister) {
		t.Fatalf("expected errReRegister, got %v", err)
	}
}

func TestWatchCommandsLoopReturnsReRegisterOnUnauthenticatedStream(t *testing.T) {
	client := &fakeAgentServiceClient{
		stream: &fakeWatchCommandsClient{
			recvErr: status.Error(codes.Unauthenticated, "stale session"),
		},
	}

	err := watchCommandsLoop(context.Background(), client, &testRuntimeConfig, agentState{
		NodeID:       "node-1",
		SessionToken: "stale-token",
	})
	if !errors.Is(err, errReRegister) {
		t.Fatalf("expected errReRegister, got %v", err)
	}
}

var testRuntimeConfig = func() agentcfg.RuntimeConfig {
	return agentcfg.RuntimeConfig{
		Server: agentcfg.ServerConfig{
			Address:  "manager.example.com:443",
			Token:    "agent-secret",
			Insecure: true,
		},
	}
}()

type fakeAgentServiceClient struct {
	registerResp *jcmanagerpb.RegisterResponse
	registerErr  error
	heartbeatErr error
	stream       grpc.ServerStreamingClient[jcmanagerpb.Command]
	watchErr     error
	reportResult *jcmanagerpb.ReportResultRequest
	reportErr    error
}

func (f *fakeAgentServiceClient) Register(ctx context.Context, in *jcmanagerpb.RegisterRequest, opts ...grpc.CallOption) (*jcmanagerpb.RegisterResponse, error) {
	return f.registerResp, f.registerErr
}

func (f *fakeAgentServiceClient) Heartbeat(ctx context.Context, in *jcmanagerpb.HeartbeatRequest, opts ...grpc.CallOption) (*jcmanagerpb.Ack, error) {
	if f.heartbeatErr != nil {
		return nil, f.heartbeatErr
	}
	return &jcmanagerpb.Ack{Ok: true, Message: "ok"}, nil
}

func (f *fakeAgentServiceClient) WatchCommands(ctx context.Context, in *jcmanagerpb.WatchCommandsRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[jcmanagerpb.Command], error) {
	if f.watchErr != nil {
		return nil, f.watchErr
	}
	return f.stream, nil
}

func (f *fakeAgentServiceClient) ReportResult(ctx context.Context, in *jcmanagerpb.ReportResultRequest, opts ...grpc.CallOption) (*jcmanagerpb.Ack, error) {
	f.reportResult = in
	if f.reportErr != nil {
		return nil, f.reportErr
	}
	return &jcmanagerpb.Ack{Ok: true, Message: "ok"}, nil
}

type fakeWatchCommandsClient struct {
	recvCommand *jcmanagerpb.Command
	recvErr     error
}

func (f *fakeWatchCommandsClient) Header() (metadata.MD, error) { return metadata.MD{}, nil }
func (f *fakeWatchCommandsClient) Trailer() metadata.MD         { return metadata.MD{} }
func (f *fakeWatchCommandsClient) CloseSend() error             { return nil }
func (f *fakeWatchCommandsClient) Context() context.Context     { return context.Background() }
func (f *fakeWatchCommandsClient) SendMsg(any) error            { return nil }
func (f *fakeWatchCommandsClient) RecvMsg(any) error            { return io.EOF }

func (f *fakeWatchCommandsClient) Recv() (*jcmanagerpb.Command, error) {
	if f.recvErr != nil {
		return nil, f.recvErr
	}
	if f.recvCommand == nil {
		return nil, io.EOF
	}
	command := f.recvCommand
	f.recvCommand = nil
	return command, nil
}

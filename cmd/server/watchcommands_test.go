package main

import (
	"context"
	"errors"
	"testing"

	jcmanagerpb "jcmanager/proto"

	"google.golang.org/grpc/metadata"
)

func TestWatchCommandsKeepsCommandQueuedWhenSendFails(t *testing.T) {
	srv := newTestServer(t)
	seedNode(t, srv, "node-1")

	command := &jcmanagerpb.Command{
		CommandId: "cmd-1",
		Type:      jcmanagerpb.CommandType_COMMAND_TYPE_FILE_WRITE,
		Payload: &jcmanagerpb.Command_FileWrite{
			FileWrite: &jcmanagerpb.FileWriteCommand{
				Path:    "/etc/xrayr/config.yml",
				Content: []byte("new-config"),
			},
		},
	}
	srv.dispatcher.queue("node-1").push(command)

	failStream := &fakeWatchCommandsServer{
		ctx:     context.Background(),
		sendErr: errors.New("stream dropped"),
	}
	err := srv.WatchCommands(&jcmanagerpb.WatchCommandsRequest{
		NodeId:       "node-1",
		SessionToken: "session-node-1",
	}, failStream)
	if err == nil {
		t.Fatalf("expected send failure")
	}

	retryCtx, cancel := context.WithCancel(context.Background())
	retryStream := &fakeWatchCommandsServer{
		ctx:    retryCtx,
		sentCh: make(chan *jcmanagerpb.Command, 1),
		onSend: cancel,
	}
	if err := srv.WatchCommands(&jcmanagerpb.WatchCommandsRequest{
		NodeId:       "node-1",
		SessionToken: "session-node-1",
	}, retryStream); err != nil {
		t.Fatalf("retry watch returned error: %v", err)
	}

	select {
	case resent := <-retryStream.sentCh:
		if resent.GetCommandId() != "cmd-1" {
			t.Fatalf("unexpected command resent: %#v", resent)
		}
	default:
		t.Fatalf("expected command to remain queued for retry")
	}
}

type fakeWatchCommandsServer struct {
	ctx     context.Context
	sentCh  chan *jcmanagerpb.Command
	sendErr error
	onSend  func()
}

func (f *fakeWatchCommandsServer) Send(command *jcmanagerpb.Command) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	if f.sentCh != nil {
		f.sentCh <- command
	}
	if f.onSend != nil {
		f.onSend()
	}
	return nil
}

func (f *fakeWatchCommandsServer) SetHeader(metadata.MD) error  { return nil }
func (f *fakeWatchCommandsServer) SendHeader(metadata.MD) error { return nil }
func (f *fakeWatchCommandsServer) SetTrailer(metadata.MD)       {}
func (f *fakeWatchCommandsServer) Context() context.Context     { return f.ctx }
func (f *fakeWatchCommandsServer) SendMsg(any) error            { return nil }
func (f *fakeWatchCommandsServer) RecvMsg(any) error            { return nil }

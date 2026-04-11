package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jcmanagerpb "jcmanager/proto"

	"github.com/gin-gonic/gin"
)

func TestPostNodeConfigQueuesFileWriteCommand(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)
	seedNode(t, srv, "node-1")

	router := newTestAPIRouter(srv)

	body := `{"path":"/etc/xrayr/config.yml","content":"new-config","service_name":"xrayr","create_backup":true,"restart_after_write":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/config", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer api-secret")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}

	var task configTaskResponse
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode task response: %v", err)
	}
	if task.ID == "" || task.Type != "single_config" {
		t.Fatalf("unexpected task response: %#v", task)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	command, err := srv.dispatcher.queue("node-1").pop(ctx)
	if err != nil {
		t.Fatalf("pop queued command: %v", err)
	}
	fileWrite := command.GetFileWrite()
	if fileWrite == nil {
		t.Fatalf("expected file_write command, got %#v", command)
	}
	if fileWrite.GetPath() != "/etc/xrayr/config.yml" || string(fileWrite.GetContent()) != "new-config" {
		t.Fatalf("unexpected file write payload: %#v", fileWrite)
	}
	if !fileWrite.GetCreateBackup() || !fileWrite.GetRestartAfterWrite() || fileWrite.GetServiceName() != "xrayr" {
		t.Fatalf("unexpected file write options: %#v", fileWrite)
	}
}

func TestBatchConfigCanarySuccessDispatchesNextWave(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)
	nodeIDs := make([]string, 0, 40)
	for i := 1; i <= 40; i++ {
		nodeID := fmt.Sprintf("node-%02d", i)
		nodeIDs = append(nodeIDs, nodeID)
		seedNode(t, srv, nodeID)
	}

	task, err := srv.createConfigTask(context.Background(), nodeIDs, configPushRequest{
		Path:              "/etc/xrayr/config.yml",
		Content:           "batched-config",
		ServiceName:       "xrayr",
		CreateBackup:      boolPtr(true),
		RestartAfterWrite: boolPtr(true),
	}, true)
	if err != nil {
		t.Fatalf("create batch task: %v", err)
	}
	if task.CanarySize != 2 {
		t.Fatalf("expected canary size 2, got %#v", task)
	}
	if task.InFlightNodes != 2 {
		t.Fatalf("expected initial in-flight nodes to match canary size, got %#v", task)
	}

	first := drainQueuedCommand(t, srv, nodeIDs[0])
	second := drainQueuedCommand(t, srv, nodeIDs[1])

	srv.tasks.applyCommandResult(&jcmanagerpb.CommandResult{
		NodeId:    nodeIDs[0],
		CommandId: first.GetCommandId(),
		Type:      jcmanagerpb.CommandType_COMMAND_TYPE_FILE_WRITE,
		Status:    jcmanagerpb.ResultStatus_RESULT_STATUS_SUCCESS,
		Message:   "ok",
	}, srv.dispatcher)
	srv.tasks.applyCommandResult(&jcmanagerpb.CommandResult{
		NodeId:    nodeIDs[1],
		CommandId: second.GetCommandId(),
		Type:      jcmanagerpb.CommandType_COMMAND_TYPE_FILE_WRITE,
		Status:    jcmanagerpb.ResultStatus_RESULT_STATUS_SUCCESS,
		Message:   "ok",
	}, srv.dispatcher)

	snapshot, ok := srv.tasks.snapshot(task.ID)
	if !ok {
		t.Fatalf("task snapshot missing")
	}
	if snapshot.InFlightNodes != maxBatchConfigConcurrency {
		t.Fatalf("expected second wave of %d nodes after canary, got %#v", maxBatchConfigConcurrency, snapshot)
	}
	if snapshot.PendingNodes != len(nodeIDs)-2-maxBatchConfigConcurrency {
		t.Fatalf("unexpected pending count after canary success: %#v", snapshot)
	}
}

func TestEventsEndpointStreamsSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)
	seedNode(t, srv, "node-1")
	task, err := srv.createConfigTask(context.Background(), []string{"node-1"}, configPushRequest{
		Path:              "/etc/xrayr/config.yml",
		Content:           "stream-me",
		ServiceName:       "xrayr",
		CreateBackup:      boolPtr(true),
		RestartAfterWrite: boolPtr(true),
	}, false)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	router := newTestAPIRouter(srv)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events?task_id="+task.ID, nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer api-secret")

	w := &closeNotifyRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closeCh:          make(chan bool, 1),
	}
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(w, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	w.closeCh <- true
	cancel()
	<-done

	if !strings.Contains(w.Body.String(), "event:snapshot") {
		t.Fatalf("expected snapshot event, got %q", w.Body.String())
	}
}

func seedNode(t *testing.T, srv *server, nodeID string) {
	t.Helper()

	record := nodeRecord{
		ID:               nodeID,
		DisplayName:      nodeID,
		Hostname:         nodeID,
		SessionTokenHash: hashToken("session-" + nodeID),
	}
	if err := srv.db.Create(&record).Error; err != nil {
		t.Fatalf("seed node %s: %v", nodeID, err)
	}
}

func drainQueuedCommand(t *testing.T, srv *server, nodeID string) *jcmanagerpb.Command {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	command, err := srv.dispatcher.queue(nodeID).pop(ctx)
	if err != nil {
		t.Fatalf("pop queued command for %s: %v", nodeID, err)
	}
	return command
}

func boolPtr(v bool) *bool {
	return &v
}

type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	closeCh chan bool
}

func (r *closeNotifyRecorder) CloseNotify() <-chan bool {
	return r.closeCh
}

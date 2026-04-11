package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	jcmanagerpb "jcmanager/proto"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	defaultConfigCommandTimeoutSeconds = 120
	maxBatchConfigConcurrency          = 10
	taskSubscriberBuffer               = 128
)

var localIDCounter atomic.Uint64

type configPushRequest struct {
	Path              string `json:"path"`
	Content           string `json:"content"`
	ServiceName       string `json:"service_name"`
	CreateBackup      *bool  `json:"create_backup"`
	RestartAfterWrite *bool  `json:"restart_after_write"`
}

type batchConfigRequest struct {
	NodeIDs    []string `json:"node_ids"`
	CanaryMode bool     `json:"canary_mode"`
	configPushRequest
}

type commandDispatcher struct {
	mu     sync.Mutex
	queues map[string]*nodeCommandQueue
}

type nodeCommandQueue struct {
	mu       sync.Mutex
	commands []*jcmanagerpb.Command
	notify   chan struct{}
}

type taskStore struct {
	mu            sync.Mutex
	tasks         map[string]*configTask
	commandToTask map[string]string
	subscribers   map[uint64]*taskSubscriber
	nextSubID     uint64
}

type taskSubscriber struct {
	taskID string
	ch     chan taskEvent
}

type configTask struct {
	ID                string
	Type              string
	Status            string
	Path              string
	Content           string
	ServiceName       string
	CreateBackup      bool
	RestartAfterWrite bool
	MaxConcurrency    int
	CanaryMode        bool
	CanarySize        int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Pending           []string
	InFlight          map[string]struct{}
	Nodes             map[string]*taskNodeState
	CanaryNodes       map[string]struct{}
	CanaryDone        bool
	CanaryCompleted   int
}

type taskNodeState struct {
	NodeID       string
	CommandID    string
	Status       string
	ResultStatus string
	Message      string
	BackupPath   string
	Changed      bool
	UpdatedAt    time.Time
}

type queuedCommand struct {
	taskID    string
	nodeID    string
	commandID string
	command   *jcmanagerpb.Command
}

type taskEvent struct {
	Event   string              `json:"event"`
	TaskID  string              `json:"task_id"`
	Time    time.Time           `json:"time"`
	Message string              `json:"message,omitempty"`
	Task    *configTaskResponse `json:"task,omitempty"`
	Node    *taskNodeResponse   `json:"node,omitempty"`
}

type configTaskResponse struct {
	ID                string             `json:"id"`
	Type              string             `json:"type"`
	Status            string             `json:"status"`
	Path              string             `json:"path"`
	ServiceName       string             `json:"service_name"`
	CreateBackup      bool               `json:"create_backup"`
	RestartAfterWrite bool               `json:"restart_after_write"`
	MaxConcurrency    int                `json:"max_concurrency"`
	CanaryMode        bool               `json:"canary_mode"`
	CanarySize        int                `json:"canary_size"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	TotalNodes        int                `json:"total_nodes"`
	PendingNodes      int                `json:"pending_nodes"`
	InFlightNodes     int                `json:"in_flight_nodes"`
	SucceededNodes    int                `json:"succeeded_nodes"`
	FailedNodes       int                `json:"failed_nodes"`
	SkippedNodes      int                `json:"skipped_nodes"`
	Nodes             []taskNodeResponse `json:"nodes"`
}

type taskNodeResponse struct {
	NodeID       string    `json:"node_id"`
	CommandID    string    `json:"command_id,omitempty"`
	Status       string    `json:"status"`
	ResultStatus string    `json:"result_status,omitempty"`
	Message      string    `json:"message,omitempty"`
	BackupPath   string    `json:"backup_path,omitempty"`
	Changed      bool      `json:"changed"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func newCommandDispatcher() *commandDispatcher {
	return &commandDispatcher{
		queues: make(map[string]*nodeCommandQueue),
	}
}

func newNodeCommandQueue() *nodeCommandQueue {
	return &nodeCommandQueue{
		notify: make(chan struct{}, 1),
	}
}

func (d *commandDispatcher) queue(nodeID string) *nodeCommandQueue {
	d.mu.Lock()
	defer d.mu.Unlock()

	queue, ok := d.queues[nodeID]
	if !ok {
		queue = newNodeCommandQueue()
		d.queues[nodeID] = queue
	}
	return queue
}

func (q *nodeCommandQueue) push(command *jcmanagerpb.Command) {
	q.mu.Lock()
	q.commands = append(q.commands, command)
	q.mu.Unlock()

	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *nodeCommandQueue) pop(ctx context.Context) (*jcmanagerpb.Command, error) {
	for {
		q.mu.Lock()
		if len(q.commands) > 0 {
			command := q.commands[0]
			q.commands = q.commands[1:]
			q.mu.Unlock()
			return command, nil
		}
		q.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-q.notify:
		}
	}
}

func (q *nodeCommandQueue) peek(ctx context.Context) (*jcmanagerpb.Command, error) {
	for {
		q.mu.Lock()
		if len(q.commands) > 0 {
			command := q.commands[0]
			q.mu.Unlock()
			return command, nil
		}
		q.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-q.notify:
		}
	}
}

func (q *nodeCommandQueue) ack(command *jcmanagerpb.Command) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.commands) == 0 || q.commands[0] != command {
		return
	}
	q.commands = q.commands[1:]
}

func newTaskStore() *taskStore {
	return &taskStore{
		tasks:         make(map[string]*configTask),
		commandToTask: make(map[string]string),
		subscribers:   make(map[uint64]*taskSubscriber),
	}
}

func nextLocalID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UTC().UnixNano(), localIDCounter.Add(1))
}

func (r *configPushRequest) normalize() error {
	r.Path = strings.TrimSpace(r.Path)
	r.ServiceName = strings.TrimSpace(r.ServiceName)
	if r.Path == "" {
		return fmt.Errorf("path is required")
	}

	createBackup := true
	if r.CreateBackup != nil {
		createBackup = *r.CreateBackup
	}
	restartAfterWrite := true
	if r.RestartAfterWrite != nil {
		restartAfterWrite = *r.RestartAfterWrite
	}
	if restartAfterWrite && r.ServiceName == "" {
		return fmt.Errorf("service_name is required when restart_after_write is true")
	}

	r.CreateBackup = &createBackup
	r.RestartAfterWrite = &restartAfterWrite
	return nil
}

func normalizeNodeIDs(nodeIDs []string) []string {
	seen := make(map[string]struct{}, len(nodeIDs))
	out := make([]string, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			continue
		}
		if _, ok := seen[nodeID]; ok {
			continue
		}
		seen[nodeID] = struct{}{}
		out = append(out, nodeID)
	}
	return out
}

func (s *server) handlePushNodeConfig(c *gin.Context) {
	nodeID := strings.TrimSpace(c.Param("id"))
	if nodeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node id is required"})
		return
	}

	var req configPushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := req.normalize(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	task, err := s.createConfigTask(c.Request.Context(), []string{nodeID}, req, false)
	if err != nil {
		s.writeTaskError(c, err)
		return
	}

	c.JSON(http.StatusAccepted, task)
}

func (s *server) handleBatchConfig(c *gin.Context) {
	var req batchConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := req.configPushRequest.normalize(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	nodeIDs := normalizeNodeIDs(req.NodeIDs)
	if len(nodeIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node_ids must contain at least one node"})
		return
	}

	task, err := s.createConfigTask(c.Request.Context(), nodeIDs, req.configPushRequest, req.CanaryMode)
	if err != nil {
		s.writeTaskError(c, err)
		return
	}

	c.JSON(http.StatusAccepted, task)
}

func (s *server) handleEvents(c *gin.Context) {
	taskID := strings.TrimSpace(c.Query("task_id"))
	if taskID != "" {
		if _, ok := s.tasks.snapshot(taskID); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
			return
		}
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	subscriberID, events := s.tasks.subscribe(taskID)
	defer s.tasks.unsubscribe(subscriberID)

	if taskID != "" {
		snapshot, _ := s.tasks.snapshot(taskID)
		c.SSEvent("snapshot", taskEvent{
			Event:  "snapshot",
			TaskID: taskID,
			Time:   time.Now().UTC(),
			Task:   snapshot,
		})
		c.Writer.Flush()
	} else {
		c.SSEvent("connected", taskEvent{
			Event:   "connected",
			Time:    time.Now().UTC(),
			Message: "listening for task events",
		})
		c.Writer.Flush()
	}

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case event, ok := <-events:
			if !ok {
				return false
			}
			c.SSEvent(event.Event, event)
			return true
		case <-keepAlive.C:
			c.SSEvent("keepalive", taskEvent{
				Event: "keepalive",
				Time:  time.Now().UTC(),
			})
			return true
		}
	})
}

func (s *server) createConfigTask(ctx context.Context, nodeIDs []string, req configPushRequest, canaryMode bool) (*configTaskResponse, error) {
	nodeIDs = normalizeNodeIDs(nodeIDs)
	for _, nodeID := range nodeIDs {
		if _, err := s.loadNodeRecord(ctx, nodeID); err != nil {
			if errorsIsNotFound(err) {
				return nil, fmt.Errorf("node %s not found", nodeID)
			}
			return nil, err
		}
	}

	now := time.Now().UTC()
	task := &configTask{
		ID:                nextLocalID("task"),
		Type:              "batch_config",
		Status:            "pending",
		Path:              req.Path,
		Content:           req.Content,
		ServiceName:       req.ServiceName,
		CreateBackup:      *req.CreateBackup,
		RestartAfterWrite: *req.RestartAfterWrite,
		MaxConcurrency:    minInt(maxBatchConfigConcurrency, len(nodeIDs)),
		CanaryMode:        canaryMode,
		CreatedAt:         now,
		UpdatedAt:         now,
		Pending:           append([]string(nil), nodeIDs...),
		InFlight:          make(map[string]struct{}),
		Nodes:             make(map[string]*taskNodeState, len(nodeIDs)),
		CanaryNodes:       make(map[string]struct{}),
	}
	if len(nodeIDs) == 1 {
		task.Type = "single_config"
	}
	if task.MaxConcurrency == 0 {
		task.MaxConcurrency = 1
	}
	if canaryMode {
		task.CanarySize = maxInt(1, int((len(nodeIDs)+19)/20))
		for _, nodeID := range nodeIDs[:task.CanarySize] {
			task.CanaryNodes[nodeID] = struct{}{}
		}
	} else {
		task.CanaryDone = true
	}
	for _, nodeID := range nodeIDs {
		task.Nodes[nodeID] = &taskNodeState{
			NodeID:    nodeID,
			Status:    "pending",
			Message:   "waiting to be queued",
			UpdatedAt: now,
		}
	}

	s.tasks.mu.Lock()
	s.tasks.tasks[task.ID] = task
	createdEvent := taskEvent{
		Event:   "task_created",
		TaskID:  task.ID,
		Time:    now,
		Message: "task created",
		Task:    task.toResponseLocked(),
	}
	dispatches, queueEvents := s.tasks.dispatchReadyCommandsLocked(task, req)
	snapshot := task.toResponseLocked()
	s.tasks.mu.Unlock()

	s.tasks.publish(createdEvent)
	s.enqueueCommands(dispatches)
	s.tasks.publish(queueEvents...)
	return snapshot, nil
}

func (s *server) enqueueCommands(dispatches []queuedCommand) {
	for _, dispatch := range dispatches {
		s.dispatcher.queue(dispatch.nodeID).push(dispatch.command)
	}
}

func (ts *taskStore) dispatchReadyCommandsLocked(task *configTask, req configPushRequest) ([]queuedCommand, []taskEvent) {
	if task.Status == "failed" || task.Status == "succeeded" {
		return nil, nil
	}

	var (
		dispatches []queuedCommand
		events     []taskEvent
	)
	for len(task.InFlight) < task.MaxConcurrency {
		nodeID, ok := nextNodeForDispatchLocked(task)
		if !ok {
			break
		}

		now := time.Now().UTC()
		commandID := nextLocalID("cmd")
		command := &jcmanagerpb.Command{
			CommandId:      commandID,
			Type:           jcmanagerpb.CommandType_COMMAND_TYPE_FILE_WRITE,
			IssuedAtUnix:   now.Unix(),
			TimeoutSeconds: defaultConfigCommandTimeoutSeconds,
			Payload: &jcmanagerpb.Command_FileWrite{
				FileWrite: &jcmanagerpb.FileWriteCommand{
					Path:              req.Path,
					Content:           []byte(req.Content),
					CreateBackup:      *req.CreateBackup,
					RestartAfterWrite: *req.RestartAfterWrite,
					ServiceName:       req.ServiceName,
				},
			},
		}

		task.Status = "running"
		task.UpdatedAt = now
		task.InFlight[nodeID] = struct{}{}
		node := task.Nodes[nodeID]
		node.CommandID = commandID
		node.Status = "queued"
		node.Message = "command queued"
		node.UpdatedAt = now
		ts.commandToTask[commandID] = task.ID

		dispatches = append(dispatches, queuedCommand{
			taskID:    task.ID,
			nodeID:    nodeID,
			commandID: commandID,
			command:   command,
		})
		events = append(events, taskEvent{
			Event:   "node_queued",
			TaskID:  task.ID,
			Time:    now,
			Message: "node command queued",
			Task:    task.toResponseLocked(),
			Node:    node.toResponse(),
		})
	}

	return dispatches, events
}

func nextNodeForDispatchLocked(task *configTask) (string, bool) {
	if task.CanaryMode && !task.CanaryDone {
		for idx, nodeID := range task.Pending {
			if _, ok := task.CanaryNodes[nodeID]; ok {
				task.Pending = append(task.Pending[:idx], task.Pending[idx+1:]...)
				return nodeID, true
			}
		}
		if task.CanaryCompleted < task.CanarySize {
			return "", false
		}
		task.CanaryDone = true
	}

	if len(task.Pending) == 0 {
		return "", false
	}

	nodeID := task.Pending[0]
	task.Pending = task.Pending[1:]
	return nodeID, true
}

func (ts *taskStore) applyCommandResult(result *jcmanagerpb.CommandResult, dispatcher *commandDispatcher) {
	ts.mu.Lock()

	taskID, ok := ts.commandToTask[strings.TrimSpace(result.GetCommandId())]
	if !ok {
		ts.mu.Unlock()
		return
	}
	task, ok := ts.tasks[taskID]
	if !ok {
		delete(ts.commandToTask, strings.TrimSpace(result.GetCommandId()))
		ts.mu.Unlock()
		return
	}

	delete(ts.commandToTask, strings.TrimSpace(result.GetCommandId()))

	node := task.Nodes[strings.TrimSpace(result.GetNodeId())]
	if node == nil {
		ts.mu.Unlock()
		return
	}

	now := time.Now().UTC()
	delete(task.InFlight, node.NodeID)

	success := result.GetStatus() == jcmanagerpb.ResultStatus_RESULT_STATUS_SUCCESS
	if success {
		node.Status = "succeeded"
	} else {
		node.Status = "failed"
	}
	node.ResultStatus = strings.ToLower(strings.TrimPrefix(result.GetStatus().String(), "RESULT_STATUS_"))
	node.Message = strings.TrimSpace(firstNonEmpty(result.GetMessage(), result.GetStderr(), result.GetStdout()))
	node.BackupPath = strings.TrimSpace(result.GetBackupPath())
	node.Changed = result.GetChanged()
	node.UpdatedAt = now
	task.UpdatedAt = now

	var events []taskEvent

	if task.CanaryMode && !task.CanaryDone {
		if _, isCanary := task.CanaryNodes[node.NodeID]; isCanary {
			task.CanaryCompleted++
			if !success {
				skipped := haltPendingNodesLocked(task, "rollout halted after canary failure", now)
				events = append(events, taskEvent{
					Event:   "task_halted",
					TaskID:  task.ID,
					Time:    now,
					Message: "canary failure halted rollout",
					Task:    task.toResponseLocked(),
					Node:    node.toResponse(),
				})
				for _, skippedNode := range skipped {
					events = append(events, taskEvent{
						Event:   "node_skipped",
						TaskID:  task.ID,
						Time:    now,
						Message: skippedNode.Message,
						Task:    task.toResponseLocked(),
						Node:    skippedNode.toResponse(),
					})
				}
			}
			if success && task.CanaryCompleted >= task.CanarySize {
				task.CanaryDone = true
			}
		}
	}

	dispatches, queueEvents := ts.dispatchReadyCommandsLocked(task, configPushRequest{
		Path:              task.Path,
		Content:           task.Content,
		ServiceName:       task.ServiceName,
		CreateBackup:      &task.CreateBackup,
		RestartAfterWrite: &task.RestartAfterWrite,
	})
	events = append(events, taskEvent{
		Event:   "node_result",
		TaskID:  task.ID,
		Time:    now,
		Message: "node reported result",
		Task:    task.toResponseLocked(),
		Node:    node.toResponse(),
	})
	if len(queueEvents) > 0 {
		events = append(events, queueEvents...)
	}

	if len(task.Pending) == 0 && len(task.InFlight) == 0 {
		task.Status = finalTaskStatus(task)
		task.UpdatedAt = now
		events = append(events, taskEvent{
			Event:   "task_complete",
			TaskID:  task.ID,
			Time:    now,
			Message: "task finished",
			Task:    task.toResponseLocked(),
		})
	}

	ts.mu.Unlock()

	dispatcherQueueEvents(dispatcher, dispatches)
	ts.publish(events...)
}

func dispatcherQueueEvents(dispatcher *commandDispatcher, dispatches []queuedCommand) {
	for _, dispatch := range dispatches {
		dispatcher.queue(dispatch.nodeID).push(dispatch.command)
	}
}

func haltPendingNodesLocked(task *configTask, message string, now time.Time) []*taskNodeState {
	task.Status = "failed"
	task.UpdatedAt = now

	skipped := make([]*taskNodeState, 0, len(task.Pending))
	for _, nodeID := range task.Pending {
		node := task.Nodes[nodeID]
		if node == nil {
			continue
		}
		node.Status = "skipped"
		node.Message = message
		node.UpdatedAt = now
		skipped = append(skipped, node)
	}
	task.Pending = nil
	return skipped
}

func finalTaskStatus(task *configTask) string {
	status := "succeeded"
	for _, node := range task.Nodes {
		if node == nil {
			continue
		}
		if node.Status == "failed" {
			return "failed"
		}
		if node.Status == "skipped" {
			status = "failed"
		}
	}
	return status
}

func (ts *taskStore) subscribe(taskID string) (uint64, <-chan taskEvent) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.nextSubID++
	ch := make(chan taskEvent, taskSubscriberBuffer)
	ts.subscribers[ts.nextSubID] = &taskSubscriber{
		taskID: taskID,
		ch:     ch,
	}
	return ts.nextSubID, ch
}

func (ts *taskStore) unsubscribe(id uint64) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if subscriber, ok := ts.subscribers[id]; ok {
		delete(ts.subscribers, id)
		close(subscriber.ch)
	}
}

func (ts *taskStore) snapshot(taskID string) (*configTaskResponse, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	task, ok := ts.tasks[taskID]
	if !ok {
		return nil, false
	}
	return task.toResponseLocked(), true
}

func (ts *taskStore) publish(events ...taskEvent) {
	ts.mu.Lock()
	subscribers := make([]*taskSubscriber, 0, len(ts.subscribers))
	for _, subscriber := range ts.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	ts.mu.Unlock()

	for _, event := range events {
		for _, subscriber := range subscribers {
			if subscriber.taskID != "" && subscriber.taskID != event.TaskID {
				continue
			}
			select {
			case subscriber.ch <- event:
			default:
			}
		}
	}
}

func (task *configTask) toResponseLocked() *configTaskResponse {
	nodes := make([]taskNodeResponse, 0, len(task.Nodes))
	var (
		pending   int
		succeeded int
		failed    int
		skipped   int
	)
	for _, node := range task.Nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case "pending":
			pending++
		case "succeeded":
			succeeded++
		case "failed":
			failed++
		case "skipped":
			skipped++
		}
		nodes = append(nodes, *node.toResponse())
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})

	return &configTaskResponse{
		ID:                task.ID,
		Type:              task.Type,
		Status:            task.Status,
		Path:              task.Path,
		ServiceName:       task.ServiceName,
		CreateBackup:      task.CreateBackup,
		RestartAfterWrite: task.RestartAfterWrite,
		MaxConcurrency:    task.MaxConcurrency,
		CanaryMode:        task.CanaryMode,
		CanarySize:        task.CanarySize,
		CreatedAt:         task.CreatedAt,
		UpdatedAt:         task.UpdatedAt,
		TotalNodes:        len(task.Nodes),
		PendingNodes:      pending,
		InFlightNodes:     len(task.InFlight),
		SucceededNodes:    succeeded,
		FailedNodes:       failed,
		SkippedNodes:      skipped,
		Nodes:             nodes,
	}
}

func (node *taskNodeState) toResponse() *taskNodeResponse {
	return &taskNodeResponse{
		NodeID:       node.NodeID,
		CommandID:    node.CommandID,
		Status:       node.Status,
		ResultStatus: node.ResultStatus,
		Message:      node.Message,
		BackupPath:   node.BackupPath,
		Changed:      node.Changed,
		UpdatedAt:    node.UpdatedAt,
	}
}

func (s *server) writeTaskError(c *gin.Context, err error) {
	statusCode := http.StatusInternalServerError
	if strings.Contains(err.Error(), "not found") {
		statusCode = http.StatusNotFound
	} else if strings.Contains(err.Error(), "required") {
		statusCode = http.StatusBadRequest
	}
	c.JSON(statusCode, gin.H{"error": err.Error()})
}

func errorsIsNotFound(err error) bool {
	return err != nil && errors.Is(err, gorm.ErrRecordNotFound)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"icloud-hme/internal/account"
	"icloud-hme/internal/hme"
)

const generationTaskRecentLimit = 200

var defaultGenerationFailureDelays = []time.Duration{
	time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	time.Hour,
}

var errGenerationTaskStopping = errors.New("持续生成任务正在停止")

type generationTaskOptions struct {
	statePath     string
	successDelay  time.Duration
	failureDelays []time.Duration
}

type generationTaskState struct {
	AccountID           string             `json:"account_id"`
	LabelPrefix         string             `json:"label_prefix"`
	Running             bool               `json:"running"`
	State               string             `json:"state"`
	Created             int                `json:"created"`
	Attempts            int                `json:"attempts"`
	FailureCount        int                `json:"failure_count"`
	ConsecutiveFailures int                `json:"consecutive_failures"`
	StartedAt           string             `json:"started_at,omitempty"`
	UpdatedAt           string             `json:"updated_at,omitempty"`
	NextRunAt           string             `json:"next_run_at,omitempty"`
	LastSuccessAt       string             `json:"last_success_at,omitempty"`
	Message             string             `json:"message,omitempty"`
	Aliases             []hme.CreateResult `json:"aliases,omitempty"`
}

type generationTaskRuntime struct {
	state        generationTaskState
	cancel       context.CancelFunc
	workerActive bool
	runID        uint64
}

type generationTaskManager struct {
	mu            sync.Mutex
	be            Backend
	statePath     string
	successDelay  time.Duration
	failureDelays []time.Duration
	tasks         map[string]*generationTaskRuntime
}

func newGenerationTaskManager(be Backend, opts generationTaskOptions) *generationTaskManager {
	if opts.successDelay <= 0 {
		opts.successDelay = 10 * time.Second
	}
	if len(opts.failureDelays) == 0 {
		opts.failureDelays = defaultGenerationFailureDelays
	}
	m := &generationTaskManager{
		be:            be,
		statePath:     opts.statePath,
		successDelay:  opts.successDelay,
		failureDelays: append([]time.Duration(nil), opts.failureDelays...),
		tasks:         make(map[string]*generationTaskRuntime),
	}
	m.load()
	m.resumeRunningTasks()
	return m
}

func (m *generationTaskManager) load() {
	if m.statePath == "" {
		return
	}
	raw, err := os.ReadFile(m.statePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("读取持续生成任务状态失败: %v", err)
		}
		return
	}
	var stored struct {
		Tasks map[string]generationTaskState `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		log.Printf("解析持续生成任务状态失败: %v", err)
		return
	}
	for accountID, state := range stored.Tasks {
		if state.AccountID == "" {
			state.AccountID = accountID
		}
		if state.State == "" {
			state.State = "stopped"
		}
		m.tasks[accountID] = &generationTaskRuntime{state: state}
	}
}

func (m *generationTaskManager) resumeRunningTasks() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for accountID, task := range m.tasks {
		if !task.state.Running {
			continue
		}
		task.state.State = "cooldown"
		if task.state.NextRunAt == "" {
			task.state.NextRunAt = time.Now().Format(time.RFC3339)
		}
		m.startWorkerLocked(accountID, task)
	}
}

func (m *generationTaskManager) Start(accountID, labelPrefix string) (generationTaskState, error) {
	labelPrefix = strings.TrimSpace(labelPrefix)
	if labelPrefix == "" {
		labelPrefix = "持续邮箱-" + time.Now().Format("20060102-150405")
	}
	if len([]rune(labelPrefix)) > 160 {
		return generationTaskState{}, fmt.Errorf("label_prefix 最长 160 字符")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[accountID]
	if task == nil {
		task = &generationTaskRuntime{}
		m.tasks[accountID] = task
	}
	if task.workerActive {
		if task.state.Running {
			return cloneGenerationTaskState(task.state), nil
		}
		return cloneGenerationTaskState(task.state), errGenerationTaskStopping
	}

	now := time.Now().Format(time.RFC3339)
	task.state = generationTaskState{
		AccountID:   accountID,
		LabelPrefix: labelPrefix,
		Running:     true,
		State:       "running",
		StartedAt:   now,
		UpdatedAt:   now,
		NextRunAt:   now,
		Message:     "任务已启动，准备创建邮箱",
		Aliases:     []hme.CreateResult{},
	}
	m.startWorkerLocked(accountID, task)
	m.persistLocked()
	return cloneGenerationTaskState(task.state), nil
}

func (m *generationTaskManager) Stop(accountID string) generationTaskState {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[accountID]
	if task == nil {
		return generationTaskState{AccountID: accountID, State: "stopped", Aliases: []hme.CreateResult{}}
	}
	task.state.Running = false
	task.state.NextRunAt = ""
	task.state.UpdatedAt = time.Now().Format(time.RFC3339)
	if task.workerActive {
		task.state.State = "stopping"
		task.state.Message = "正在停止当前请求"
		if task.cancel != nil {
			task.cancel()
		}
	} else {
		task.state.State = "stopped"
		task.state.Message = "任务已停止"
	}
	m.persistLocked()
	return cloneGenerationTaskState(task.state)
}

func (m *generationTaskManager) Status(accountID string) generationTaskState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if task := m.tasks[accountID]; task != nil {
		return cloneGenerationTaskState(task.state)
	}
	return generationTaskState{AccountID: accountID, State: "stopped", Aliases: []hme.CreateResult{}}
}

func (m *generationTaskManager) startWorkerLocked(accountID string, task *generationTaskRuntime) {
	ctx, cancel := context.WithCancel(context.Background())
	task.cancel = cancel
	task.workerActive = true
	task.runID++
	runID := task.runID
	go m.run(ctx, accountID, runID)
}

func (m *generationTaskManager) run(ctx context.Context, accountID string, runID uint64) {
	defer m.workerExited(accountID, runID)
	for {
		wait, ok := m.nextAttemptWait(accountID, runID)
		if !ok {
			return
		}
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
			}
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
		label, ok := m.beginAttempt(accountID, runID)
		if !ok {
			return
		}

		result, err := m.be.CreateAlias(accountID, label)
		if !m.completeAttempt(accountID, runID, result, err) {
			return
		}
	}
}

func (m *generationTaskManager) nextAttemptWait(accountID string, runID uint64) (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[accountID]
	if task == nil || task.runID != runID || !task.state.Running {
		return 0, false
	}
	wait := time.Duration(0)
	if task.state.NextRunAt != "" {
		if next, err := time.Parse(time.RFC3339, task.state.NextRunAt); err == nil && next.After(time.Now()) {
			wait = time.Until(next)
		}
	}
	return wait, true
}

func (m *generationTaskManager) beginAttempt(accountID string, runID uint64) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[accountID]
	if task == nil || task.runID != runID || !task.state.Running {
		return "", false
	}
	label := fmt.Sprintf("%s-%06d", task.state.LabelPrefix, task.state.Created+1)
	task.state.State = "running"
	task.state.Message = "正在请求创建邮箱"
	task.state.NextRunAt = ""
	task.state.Attempts++
	task.state.UpdatedAt = time.Now().Format(time.RFC3339)
	m.persistLocked()
	return label, true
}

func (m *generationTaskManager) completeAttempt(accountID string, runID uint64, result *hme.CreateResult, createErr error) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[accountID]
	if task == nil || task.runID != runID {
		return false
	}
	now := time.Now()
	delay := m.successDelay
	if createErr == nil && result != nil {
		task.state.Created++
		task.state.ConsecutiveFailures = 0
		task.state.LastSuccessAt = now.Format(time.RFC3339)
		task.state.Message = "创建成功，等待下一次请求"
		task.state.Aliases = append(task.state.Aliases, *result)
		if len(task.state.Aliases) > generationTaskRecentLimit {
			task.state.Aliases = append([]hme.CreateResult(nil), task.state.Aliases[len(task.state.Aliases)-generationTaskRecentLimit:]...)
		}
	} else {
		task.state.FailureCount++
		task.state.ConsecutiveFailures++
		delay = m.failureDelay(task.state.ConsecutiveFailures)
		task.state.Message = generationTaskErrorMessage(createErr)
	}
	task.state.UpdatedAt = now.Format(time.RFC3339)
	if !task.state.Running {
		task.state.State = "stopping"
		task.state.NextRunAt = ""
		m.persistLocked()
		return false
	}
	task.state.State = "cooldown"
	// 调度时间保留小数秒，避免短冷却被 RFC3339 的整秒截断后立即重试。
	task.state.NextRunAt = now.Add(delay).Format(time.RFC3339Nano)
	m.persistLocked()
	return true
}

func (m *generationTaskManager) workerExited(accountID string, runID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[accountID]
	if task == nil || task.runID != runID {
		return
	}
	task.workerActive = false
	task.cancel = nil
	if !task.state.Running {
		task.state.State = "stopped"
		task.state.Message = "任务已停止"
		task.state.NextRunAt = ""
		task.state.UpdatedAt = time.Now().Format(time.RFC3339)
	}
	m.persistLocked()
}

func (m *generationTaskManager) failureDelay(consecutive int) time.Duration {
	index := consecutive - 1
	if index < 0 {
		index = 0
	}
	if index >= len(m.failureDelays) {
		index = len(m.failureDelays) - 1
	}
	return m.failureDelays[index]
}

func (m *generationTaskManager) persistLocked() {
	if m.statePath == "" {
		return
	}
	stored := struct {
		Tasks map[string]generationTaskState `json:"tasks"`
	}{Tasks: make(map[string]generationTaskState, len(m.tasks))}
	for accountID, task := range m.tasks {
		stored.Tasks[accountID] = cloneGenerationTaskState(task.state)
	}
	raw, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		log.Printf("序列化持续生成任务状态失败: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0755); err != nil {
		log.Printf("创建持续生成任务目录失败: %v", err)
		return
	}
	tmp := m.statePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		log.Printf("写入持续生成任务状态失败: %v", err)
		return
	}
	if err := os.Rename(tmp, m.statePath); err != nil {
		log.Printf("替换持续生成任务状态失败: %v", err)
	}
}

func cloneGenerationTaskState(state generationTaskState) generationTaskState {
	state.Aliases = append([]hme.CreateResult(nil), state.Aliases...)
	if state.Aliases == nil {
		state.Aliases = []hme.CreateResult{}
	}
	return state
}

func generationTaskErrorMessage(err error) string {
	var backendErr *BackendError
	if errors.As(err, &backendErr) && backendErr.Message != "" {
		return backendErr.Message + "，已自动进入冷却"
	}
	return "上游暂时未接受创建请求，已自动进入冷却"
}

type generationTaskRequest struct {
	AccountID   string `json:"account_id"`
	LabelPrefix string `json:"label_prefix"`
}

func (s *Server) generationTaskStatusHandler(c *gin.Context) {
	accountID := strings.TrimSpace(c.Query("account_id"))
	if accountID == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数缺失: account_id")
		return
	}
	if _, ok := s.findGenerationAccount(accountID); !ok {
		failCode(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "账号不存在")
		return
	}
	ok(c, s.generationTaskPayload(s.generationTasks.Status(accountID)))
}

func (s *Server) startGenerationTaskHandler(c *gin.Context) {
	var req generationTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.AccountID) == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: account_id 必填")
		return
	}
	req.AccountID = strings.TrimSpace(req.AccountID)
	accountSummary, found := s.findGenerationAccount(req.AccountID)
	if !found {
		failCode(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "账号不存在")
		return
	}
	if accountSummary.Status != "active" || !accountSummary.HasCookies {
		failCode(c, http.StatusConflict, "ACCOUNT_NOT_READY", "账号需要有效 Cookie 后再启动任务")
		return
	}
	state, err := s.generationTasks.Start(req.AccountID, req.LabelPrefix)
	if err != nil {
		if errors.Is(err, errGenerationTaskStopping) {
			failCode(c, http.StatusConflict, "TASK_STOPPING", err.Error())
			return
		}
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	ok(c, s.generationTaskPayload(state))
}

func (s *Server) stopGenerationTaskHandler(c *gin.Context) {
	var req generationTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.AccountID) == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: account_id 必填")
		return
	}
	req.AccountID = strings.TrimSpace(req.AccountID)
	if _, ok := s.findGenerationAccount(req.AccountID); !ok {
		failCode(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "账号不存在")
		return
	}
	ok(c, s.generationTaskPayload(s.generationTasks.Stop(req.AccountID)))
}

func (s *Server) findGenerationAccount(accountID string) (account.Summary, bool) {
	for _, summary := range s.be.ListAccounts() {
		if summary.ID == accountID {
			return summary, true
		}
	}
	return account.Summary{}, false
}

func (s *Server) generationTaskPayload(state generationTaskState) gin.H {
	aliases := make([]gin.H, 0, len(state.Aliases))
	for _, result := range state.Aliases {
		aliases = append(aliases, s.createdAliasPayload(state.AccountID, result))
	}
	cooldownSeconds := 0
	if state.NextRunAt != "" {
		if next, err := time.Parse(time.RFC3339, state.NextRunAt); err == nil && next.After(time.Now()) {
			cooldownSeconds = int(time.Until(next).Seconds())
			if cooldownSeconds < 1 {
				cooldownSeconds = 1
			}
		}
	}
	return gin.H{
		"account_id":           state.AccountID,
		"label_prefix":         state.LabelPrefix,
		"running":              state.Running,
		"state":                state.State,
		"created":              state.Created,
		"attempts":             state.Attempts,
		"failure_count":        state.FailureCount,
		"consecutive_failures": state.ConsecutiveFailures,
		"started_at":           state.StartedAt,
		"updated_at":           state.UpdatedAt,
		"next_run_at":          state.NextRunAt,
		"last_success_at":      state.LastSuccessAt,
		"cooldown_seconds":     cooldownSeconds,
		"message":              state.Message,
		"aliases":              aliases,
	}
}

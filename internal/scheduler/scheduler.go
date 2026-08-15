package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
)

const schedulePath = "schedules"

type Task struct {
	ID        string `json:"id"`
	UserID    int64  `json:"user_id"`
	Prompt    string `json:"prompt"`
	RunAt     int64  `json:"run_at"`
	RunAtISO  string `json:"run_at_iso"`
	Timezone  string `json:"timezone"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	RanAt     int64  `json:"ran_at,omitempty"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Manager struct {
	fb           *firebase.Client
	pollInterval time.Duration
	mu           sync.Mutex
	runner       func(ctx context.Context, task *Task)
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

func New(fb *firebase.Client, pollIntervalSeconds int) *Manager {
	if pollIntervalSeconds <= 0 {
		pollIntervalSeconds = 15
	}
	return &Manager{
		fb:           fb,
		pollInterval: time.Duration(pollIntervalSeconds) * time.Second,
		stopCh:       make(chan struct{}),
	}
}

func (m *Manager) SetRunner(fn func(ctx context.Context, task *Task)) {
	m.runner = fn
}

func (m *Manager) Start(ctx context.Context) {
	if m.runner == nil {
		log.Printf("[scheduler] no runner set, scheduler will not execute tasks")
		return
	}
	m.loadAndScheduleOverdue(ctx)
	m.wg.Add(1)
	go m.loop(ctx)
}

func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

func (m *Manager) loadAndScheduleOverdue(ctx context.Context) {
	now := time.Now().Unix()
	keys := m.fb.ListKeys(ctx, schedulePath)
	if keys == nil {
		return
	}
	for _, key := range keys {
		raw := m.fb.Get(ctx, schedulePath+"/"+key)
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var task Task
		if err := json.Unmarshal(raw, &task); err != nil {
			log.Printf("[scheduler] unmarshal task %s: %v", key, err)
			continue
		}
		if task.Status != "pending" {
			continue
		}
		if task.RunAt <= now {
			go m.executeTask(ctx, &task)
		}
	}
}

func (m *Manager) loop(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkAndRun(ctx)
		}
	}
}

func (m *Manager) checkAndRun(ctx context.Context) {
	now := time.Now().Unix()
	keys := m.fb.ListKeys(ctx, schedulePath)
	if keys == nil {
		return
	}
	for _, key := range keys {
		raw := m.fb.Get(ctx, schedulePath+"/"+key)
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var task Task
		if err := json.Unmarshal(raw, &task); err != nil {
			continue
		}
		if task.Status != "pending" {
			continue
		}
		if task.RunAt <= now {
			go m.executeTask(ctx, &task)
		}
	}
}

func (m *Manager) executeTask(ctx context.Context, task *Task) {
	m.mu.Lock()
	if task.Status != "pending" {
		m.mu.Unlock()
		return
	}
	task.Status = "running"
	task.RanAt = time.Now().Unix()
	m.mu.Unlock()
	m.saveTask(ctx, task)

	if m.runner != nil {
		m.runner(ctx, task)
	}

	m.mu.Lock()
	if task.Error != "" {
		task.Status = "failed"
	} else if task.Status == "running" {
		task.Status = "done"
	}
	task.RanAt = time.Now().Unix()
	m.mu.Unlock()
	// Task terminal (done/failed) tidak disimpan lagi — node dihapus dari RTDB
	// supaya tidak menumpuk. Hasil sudah terkirim ke private chat user.
	m.removeTask(ctx, task.ID)
}

func (m *Manager) saveTask(ctx context.Context, task *Task) {
	if err := m.fb.Put(ctx, schedulePath+"/"+task.ID, task); err != nil {
		log.Printf("[scheduler] save task %s: %v", task.ID, err)
	}
}

func (m *Manager) removeTask(ctx context.Context, id string) {
	if err := m.fb.Delete(ctx, schedulePath+"/"+id); err != nil {
		log.Printf("[scheduler] delete task %s: %v", id, err)
	}
}

func (m *Manager) Schedule(ctx context.Context, userID int64, prompt string, runAt int64, tz string) (*Task, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("prompt tidak boleh kosong")
	}
	if runAt <= 0 {
		return nil, errors.New("run_at tidak valid")
	}
	id := fmt.Sprintf("%d-%d", userID, time.Now().UnixMilli())
	t := &Task{
		ID:        id,
		UserID:    userID,
		Prompt:    prompt,
		RunAt:     runAt,
		RunAtISO:  time.Unix(runAt, 0).UTC().Format(time.RFC3339),
		Timezone:  tz,
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
	}
	if err := m.fb.Put(ctx, schedulePath+"/"+id, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (m *Manager) Cancel(ctx context.Context, userID int64, id string) error {
	raw := m.fb.Get(ctx, schedulePath+"/"+id)
	if len(raw) == 0 || string(raw) == "null" {
		return errors.New("jadwal tidak ditemukan")
	}
	var task Task
	if err := json.Unmarshal(raw, &task); err != nil {
		return err
	}
	if task.UserID != userID {
		return errors.New("tidak punya akses ke jadwal ini")
	}
	if task.Status != "pending" {
		return errors.New("hanya jadwal pending yang bisa dibatalkan")
	}
	// Batalkan = hapus node dari RTDB (konsisten dengan task telat yang juga
	// dihapus setelah selesai).
	return m.fb.Delete(ctx, schedulePath+"/"+id)
}

func (m *Manager) List(ctx context.Context, userID int64) ([]*Task, error) {
	keys := m.fb.ListKeys(ctx, schedulePath)
	if keys == nil {
		return nil, nil
	}
	var out []*Task
	for _, key := range keys {
		raw := m.fb.Get(ctx, schedulePath+"/"+key)
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var task Task
		if err := json.Unmarshal(raw, &task); err != nil {
			continue
		}
		if task.UserID == userID {
			out = append(out, &task)
		}
	}
	return out, nil
}

func ParseAt(at, tz string) (int64, string, error) {
	at = strings.TrimSpace(at)
	if at == "" {
		return 0, "", errors.New("parameter at kosong")
	}
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return 0, "", fmt.Errorf("zona waktu tidak valid: %v", err)
	}
	now := time.Now()

	if strings.Contains(at, "T") {
		t, err := time.Parse(time.RFC3339, at)
		if err == nil {
			if t.Before(now) {
				return 0, "", fmt.Errorf("waktu sudah lewat: %s", t.Format(time.RFC3339))
			}
			return t.Unix(), tz, nil
		}
		at2 := strings.ReplaceAll(at, "Z", "+00:00")
		t, err = time.Parse("2006-01-02T15:04:05-07:00", at2)
		if err == nil {
			if t.Before(now) {
				return 0, "", fmt.Errorf("waktu sudah lewat: %s", t.Format(time.RFC3339))
			}
			return t.Unix(), tz, nil
		}
	}

	if strings.Contains(at, " ") {
		formats := []string{
			"2006-01-02 15:04:05",
			"2006-01-02 15:04",
			"2006-01-02",
		}
		for _, f := range formats {
			t, err := time.ParseInLocation(f, at, loc)
			if err == nil {
				if t.Before(now) {
					return 0, "", fmt.Errorf("waktu sudah lewat: %s", t.Format("2006-01-02 15:04:05"))
				}
				return t.Unix(), tz, nil
			}
		}
		return 0, "", fmt.Errorf("format datetime tidak dikenali: %q (gunakan RFC3339 atau 'YYYY-MM-DD HH:MM')", at)
	}

	if strings.Contains(at, ":") {
		t, err := time.ParseInLocation("15:04", at, loc)
		if err != nil {
			return 0, "", fmt.Errorf("format waktu tidak valid: %q", at)
		}
		runAt := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, loc)
		if runAt.Before(now) {
			runAt = runAt.Add(24 * time.Hour)
		}
		return runAt.Unix(), tz, nil
	}

	return 0, "", fmt.Errorf("format at tidak dikenali: %q (contoh: '19:00', '2026-08-15 19:00', '2026-08-15T19:00:00+07:00')", at)
}

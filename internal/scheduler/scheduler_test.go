package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
)

type fakeRTDB struct {
	mu sync.Mutex
	db map[string]string
}

func newFakeRTDB() *fakeRTDB {
	return &fakeRTDB{db: map[string]string{}}
}

func (f *fakeRTDB) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".json")
		if r.URL.Query().Get("shallow") == "true" {
			prefix := key + "/"
			out := map[string]bool{}
			for k := range f.db {
				if k == key {
					continue
				}
				if strings.HasPrefix(k, prefix) {
					rest := k[len(prefix):]
					if i := strings.Index(rest, "/"); i < 0 {
						out[rest] = true
					} else {
						out[rest[:i]] = true
					}
				}
			}
			if len(out) == 0 {
				w.Write([]byte("null"))
				return
			}
			raw, _ := json.Marshal(out)
			w.Write(raw)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if v, ok := f.db[key]; ok {
				w.Write([]byte(v))
			} else {
				w.Write([]byte("null"))
			}
		case http.MethodPut, http.MethodPatch:
			var body any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			raw, _ := json.Marshal(body)
			f.db[key] = string(raw)
			if r.Method == http.MethodPut {
				for k := range f.db {
					if strings.HasPrefix(k, key+"/") {
						delete(f.db, k)
					}
				}
			}
			w.Write([]byte(raw))
		case http.MethodDelete:
			delete(f.db, key)
			w.Write([]byte("null"))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func testScheduler(t *testing.T, pollSec int) *Manager {
	t.Helper()
	srv := httptest.NewServer(newFakeRTDB().handler())
	t.Cleanup(srv.Close)
	fb := firebase.New(srv.URL, srv.Client())
	return New(fb, pollSec)
}

func TestParseAt(t *testing.T) {
	now := time.Now()
	future := now.AddDate(0, 0, 3)
	futureStr := future.Format("2006-01-02")
	futureRFC3339 := time.Date(future.Year(), future.Month(), future.Day(), 12, 0, 0, 0, time.UTC).Format(time.RFC3339)

	tests := []struct {
		name    string
		at, tz  string
		wantErr string
		checkFn func(t *testing.T, got int64, gotTZ string)
	}{
		{
			name: "RFC3339 with offset",
			at:   futureRFC3339,
			tz:   "UTC",
			checkFn: func(t *testing.T, got int64, gotTZ string) {
				expected := time.Date(future.Year(), future.Month(), future.Day(), 12, 0, 0, 0, time.UTC)
				if got != expected.Unix() {
					t.Errorf("want %d got %d", expected.Unix(), got)
				}
			},
		},
		{
			name: "RFC3339 Z",
			at:   futureRFC3339,
			tz:   "UTC",
			checkFn: func(t *testing.T, got int64, gotTZ string) {
				expected := time.Date(future.Year(), future.Month(), future.Day(), 12, 0, 0, 0, time.UTC)
				if got != expected.Unix() {
					t.Errorf("want %d got %d", expected.Unix(), got)
				}
			},
		},
		{
			name: "naive datetime with tz",
			at:   futureStr + " 19:00",
			tz:   "Asia/Jakarta",
			checkFn: func(t *testing.T, got int64, gotTZ string) {
				loc := mustLoadLoc("Asia/Jakarta")
				expected := time.Date(future.Year(), future.Month(), future.Day(), 19, 0, 0, 0, loc)
				if got != expected.Unix() {
					t.Errorf("want %d got %d", expected.Unix(), got)
				}
			},
		},
		{
			name: "naive datetime no tz defaults UTC",
			at:   futureStr + " 19:00",
			tz:   "",
			checkFn: func(t *testing.T, got int64, gotTZ string) {
				if gotTZ != "UTC" {
					t.Errorf("want UTC got %s", gotTZ)
				}
				expected := time.Date(future.Year(), future.Month(), future.Day(), 19, 0, 0, 0, time.UTC)
				if got != expected.Unix() {
					t.Errorf("want %d got %d", expected.Unix(), got)
				}
			},
		},
		{
			name: "time-only today",
			at:   "19:00",
			tz:   "Asia/Jakarta",
			checkFn: func(t *testing.T, got int64, gotTZ string) {
				loc := mustLoadLoc("Asia/Jakarta")
				nowLoc := time.Now().In(loc)
				expected := time.Date(nowLoc.Year(), nowLoc.Month(), nowLoc.Day(), 19, 0, 0, 0, loc)
				if expected.Before(nowLoc) {
					expected = expected.Add(24 * time.Hour)
				}
				if got != expected.Unix() {
					t.Errorf("want %d got %d", expected.Unix(), got)
				}
			},
		},
		{
			name: "time-only rolls over to tomorrow if past",
			at:   "00:00",
			tz:   "Asia/Jakarta",
			checkFn: func(t *testing.T, got int64, gotTZ string) {
				loc := mustLoadLoc("Asia/Jakarta")
				nowLoc := time.Now().In(loc)
				expected := time.Date(nowLoc.Year(), nowLoc.Month(), nowLoc.Day(), 0, 0, 0, 0, loc)
				if expected.Before(nowLoc) {
					expected = expected.Add(24 * time.Hour)
				}
				if got != expected.Unix() {
					t.Errorf("want %d got %d", expected.Unix(), got)
				}
			},
		},
		{
			name:    "empty at error",
			at:      "",
			tz:      "UTC",
			wantErr: "kosong",
		},
		{
			name:    "invalid tz error",
			at:      "19:00",
			tz:      "Invalid/Zone",
			wantErr: "zona waktu tidak valid",
		},
		{
			name:    "past datetime error",
			at:      "2020-01-01 12:00",
			tz:      "UTC",
			wantErr: "sudah lewat",
		},
		{
			name:    "unrecognized format error",
			at:      "not-a-time",
			tz:      "UTC",
			wantErr: "tidak dikenali",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, gotTZ, err := ParseAt(tc.at, tc.tz)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.checkFn != nil {
				tc.checkFn(t, got, gotTZ)
			}
		})
	}
}

func mustLoadLoc(tz string) *time.Location {
	loc, _ := time.LoadLocation(tz)
	return loc
}

func TestScheduleCancelList(t *testing.T) {
	ctx := context.Background()
	m := testScheduler(t, 1)

	// Schedule
	task, err := m.Schedule(ctx, 123, "test prompt", time.Now().Add(time.Hour).Unix(), "Asia/Jakarta")
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if task.ID == "" || task.UserID != 123 || task.Prompt != "test prompt" {
		t.Fatalf("unexpected task: %+v", task)
	}
	if task.Status != "pending" {
		t.Fatalf("want pending got %s", task.Status)
	}

	// List
	list, err := m.List(ctx, 123)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != task.ID {
		t.Fatalf("list mismatch: %+v", list)
	}

	// Cancel deletes the node from RTDB (like finished tasks).
	if err := m.Cancel(ctx, 123, task.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	raw := m.fb.Get(ctx, "schedules/"+task.ID) // access fb directly for test
	if len(raw) != 0 && string(raw) != "null" {
		t.Fatalf("want task deleted after cancel, got %s", raw)
	}

	// Cancel again should fail (node gone)
	if err := m.Cancel(ctx, 123, task.ID); err == nil {
		t.Fatalf("expected error on double cancel")
	}

	// Cancel other user's task should fail
	task2, _ := m.Schedule(ctx, 456, "other", time.Now().Add(time.Hour).Unix(), "UTC")
	if err := m.Cancel(ctx, 123, task2.ID); err == nil {
		t.Fatalf("expected error on cross-user cancel")
	}
}

func TestExecuteTaskOverdueOnStart(t *testing.T) {
	ctx := context.Background()
	m := testScheduler(t, 1)

	var executed *Task
	m.SetRunner(func(ctx context.Context, t *Task) {
		executed = t
		t.Result = "done"
	})

	// schedule in the past
	past := time.Now().Add(-time.Minute).Unix()
	_, err := m.Schedule(ctx, 123, "overdue task", past, "UTC")
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}

	// start scheduler - should pick up overdue
	m.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	m.Stop()

	if executed == nil {
		t.Fatal("task not executed")
	}
	if executed.Result != "done" {
		t.Fatalf("want result 'done' got %q", executed.Result)
	}
	// Terminal task must be deleted from RTDB.
	raw := m.fb.Get(ctx, "schedules/"+executed.ID)
	if len(raw) != 0 && string(raw) != "null" {
		t.Fatalf("want task deleted after execution, got %s", raw)
	}
}

func TestExecuteTaskFutureOnStart(t *testing.T) {
	ctx := context.Background()
	m := testScheduler(t, 1)

	var executed *Task
	m.SetRunner(func(ctx context.Context, t *Task) {
		executed = t
	})

	// schedule in the future (not due yet)
	future := time.Now().Add(time.Hour).Unix()
	_, err := m.Schedule(ctx, 123, "future task", future, "UTC")
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}

	m.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	m.Stop()

	if executed != nil {
		t.Fatal("task should not execute yet")
	}
}

func TestExecuteTaskDeletesOnError(t *testing.T) {
	ctx := context.Background()
	m := testScheduler(t, 1)

	m.SetRunner(func(ctx context.Context, t *Task) {
		t.Error = "runner error"
	})

	past := time.Now().Add(-time.Minute).Unix()
	task, err := m.Schedule(ctx, 123, "error task", past, "UTC")
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}

	m.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	m.Stop()

	// Failed tasks are also deleted (terminal state).
	raw := m.fb.Get(ctx, "schedules/"+task.ID)
	if len(raw) != 0 && string(raw) != "null" {
		t.Fatalf("want failed task deleted, got %s", raw)
	}
}

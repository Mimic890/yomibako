package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Job struct {
	ID        string
	FileName  string
	Format    string
	CreatedAt time.Time
	done      chan struct{}
	filePath  string
	err       error
}

type jobs struct {
	ttl   time.Duration
	dir   string
	mu    sync.Mutex
	items map[string]*Job
}

func newJobs(ttlMinutes int) (*jobs, error) {
	if ttlMinutes < 1 {
		ttlMinutes = 1
	}
	dir, err := os.MkdirTemp("", "yomibako-*")
	if err != nil {
		return nil, err
	}
	return &jobs{
		ttl:   time.Duration(ttlMinutes) * time.Minute,
		dir:   dir,
		items: make(map[string]*Job),
	}, nil
}

func (j *jobs) start(runtimeName string, runtime Runtime, params GrabRequest) (*Job, error) {
	if !runtime.Enabled() {
		return nil, fmt.Errorf("Runtime %q is disabled", runtimeName)
	}
	format := "epub"
	if runtimeName == "mihon" {
		format = "cbz"
	}
	job := &Job{
		ID:        newID(),
		FileName:  fmt.Sprintf("%s.%s", sanitizeName(params.Title), format),
		Format:    format,
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
	}
	outDir := filepath.Join(j.dir, job.ID)

	j.mu.Lock()
	j.items[job.ID] = job
	j.mu.Unlock()

	go j.run(runtime, params, outDir, job)
	return job, nil
}

func (j *jobs) run(runtime Runtime, params GrabRequest, outDir string, job *Job) {
	defer close(job.done)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		job.err = err
		return
	}
	result, err := runtime.Grab(params, outDir)
	if err != nil {
		job.err = err
		return
	}
	job.filePath = result.FilePath
}

func (j *jobs) get(id string) *Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.items[id]
}

func (j *jobs) sweep() {
	cutoff := time.Now().Add(-j.ttl)
	var remove []string

	j.mu.Lock()
	for id, job := range j.items {
		select {
		case <-job.done:
			if job.CreatedAt.Before(cutoff) {
				delete(j.items, id)
				remove = append(remove, id)
			}
		default:
		}
	}
	j.mu.Unlock()

	for _, id := range remove {
		_ = os.RemoveAll(filepath.Join(j.dir, id))
	}
}

func newID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}

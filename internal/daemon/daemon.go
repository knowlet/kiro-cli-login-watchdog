package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var ErrLocked = errors.New("watchdog instance or lifecycle operation is already active")

type Lock struct{ file *os.File }

// Acquire uses an OS lock, not the existence of a file. The kernel releases it
// after a crash. Never unlink lock files: doing so would create two lock inodes.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = lockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Close() error { return l.file.Close() }

type Record struct {
	PID     int    `json:"pid"`
	Address string `json:"address"`
	Token   string `json:"token"`
}

func ReadRecord(path string) (Record, error) {
	var record Record
	file, err := os.Open(path)
	if err != nil {
		return record, err
	}
	defer func() { _ = file.Close() }()
	if err = json.NewDecoder(io.LimitReader(file, 4096)).Decode(&record); err != nil {
		return Record{}, fmt.Errorf("invalid watchdog state (legacy PID-only files are not trusted)")
	}
	if err = record.validate(); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (r Record) validate() error {
	host, port, err := net.SplitHostPort(r.Address)
	n, portErr := strconv.Atoi(port)
	token, tokenErr := hex.DecodeString(r.Token)
	if err != nil || host != "127.0.0.1" || portErr != nil || n <= 0 || n > 65535 ||
		r.PID <= 0 || tokenErr != nil || len(token) != 32 {
		return errors.New("invalid watchdog control record")
	}
	return nil
}

func writeRecord(path string, record Record) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".watchdog-state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err = file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

type Instance struct {
	Record Record
	path   string
	lock   *Lock
	server *http.Server
	once   sync.Once
}

// Start acquires the lifetime lock before publishing state or running a health
// check. A random instance token authenticates control requests and responses;
// a recycled numeric PID can never authorize a stop.
func Start(path string, cancel context.CancelFunc) (*Instance, error) {
	lock, err := Acquire(path + ".lock")
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		_ = listener.Close()
		_ = lock.Close()
		return nil, err
	}
	instance := &Instance{path: path, lock: lock, Record: Record{
		PID: os.Getpid(), Address: listener.Addr().String(), Token: hex.EncodeToString(token[:]),
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/kiro-watchdog/status", instance.handler(false, cancel))
	mux.HandleFunc("/v1/kiro-watchdog/stop", instance.handler(true, cancel))
	instance.server = &http.Server{Handler: mux, ReadHeaderTimeout: time.Second,
		ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: time.Second,
		MaxHeaderBytes: 4096}
	if err = writeRecord(path, instance.Record); err != nil {
		_ = listener.Close()
		_ = lock.Close()
		return nil, err
	}
	go func() {
		if err := instance.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			cancel()
		}
	}()
	return instance, nil
}

func (i *Instance) handler(stop bool, cancel context.CancelFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		method := http.MethodGet
		if stop {
			method = http.MethodPost
		}
		if r.Method != method {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+i.Record.Token)) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(i.Record); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if stop {
			cancel()
		}
	}
}

// Close runs only after the watchdog has canceled and reaped its CLI command.
// Keep the lifetime lock through state cleanup to prevent deleting a successor.
func (i *Instance) Close() {
	i.once.Do(func() {
		_ = i.server.Close()
		if record, err := ReadRecord(i.path); err == nil && record.Token == i.Record.Token {
			_ = os.Remove(i.path)
		}
		_ = i.lock.Close()
	})
}

// Control never follows redirects or environment proxies and refuses any
// address except literal loopback. The state file must live in a private dir.
func Control(ctx context.Context, record Record, stop bool) error {
	if err := record.validate(); err != nil {
		return err
	}
	action, method := "status", http.MethodGet
	if stop {
		action, method = "stop", http.MethodPost
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+record.Address+"/v1/kiro-watchdog/"+action, nil)
	if err != nil {
		return errors.New("build watchdog control request")
	}
	req.Header.Set("Authorization", "Bearer "+record.Token)
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("watchdog control endpoint is unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	var reply Record
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&reply) != nil ||
		reply.PID != record.PID || reply.Address != record.Address ||
		subtle.ConstantTimeCompare([]byte(reply.Token), []byte(record.Token)) != 1 {
		return errors.New("watchdog instance identity could not be verified")
	}
	return nil
}

// Status checks the lifetime lock first. A stale state file, even one pointing
// at a live unrelated PID, cannot cause a process to be probed or signaled.
func Status(ctx context.Context, path string) (Record, bool, error) {
	lock, err := Acquire(path + ".lock")
	if err == nil {
		if closeErr := lock.Close(); closeErr != nil {
			return Record{}, false, fmt.Errorf("release watchdog status probe: %w", closeErr)
		}
		return Record{}, false, nil
	}
	if !errors.Is(err, ErrLocked) {
		return Record{}, false, err
	}
	record, err := ReadRecord(path)
	if err != nil {
		return Record{}, false, fmt.Errorf("watchdog lock held, state not ready: %w", err)
	}
	if err = Control(ctx, record, false); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}

func WaitStopped(ctx context.Context, path string, record Record) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		lock, err := Acquire(path + ".lock")
		if err == nil {
			if closeErr := lock.Close(); closeErr != nil {
				return fmt.Errorf("release watchdog stop probe: %w", closeErr)
			}
			return nil
		}
		if !errors.Is(err, ErrLocked) {
			return err
		}
		if current, err := ReadRecord(path); err == nil && current.Token != record.Token {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

package gateway

// 异步请求日志（MT5）：高并发下不让日志 IO 落在请求路径上。
// 有界队列 + 批量落盘；队列满时丢弃并计数（日志永远不阻塞主路径）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	logQueueSize  = 4096
	logBatchSize  = 512
	logFlushEvery = 100 * time.Millisecond
)

type logWriter struct {
	home    string
	ch      chan RequestLogEntry
	done    chan struct{}
	wg      sync.WaitGroup
	dropped atomic.Int64
	once    sync.Once
}

func newLogWriter(home string) *logWriter {
	w := &logWriter{
		home: home,
		ch:   make(chan RequestLogEntry, logQueueSize),
		done: make(chan struct{}),
	}
	w.wg.Add(1)
	go w.loop()
	return w
}

// append 非阻塞入队；队列满时丢弃（MT5：日志不影响请求延迟）。
func (w *logWriter) append(entry RequestLogEntry) {
	select {
	case w.ch <- entry:
	default:
		w.dropped.Add(1)
	}
}

// Dropped 返回被丢弃的日志条数（应为 0；持续非 0 说明队列太小或盘写太慢）。
func (w *logWriter) Dropped() int64 { return w.dropped.Load() }

func (w *logWriter) loop() {
	defer w.wg.Done()
	batch := make([]RequestLogEntry, 0, logBatchSize)
	ticker := time.NewTicker(logFlushEvery)
	defer ticker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		_ = appendRequestLogBatch(w.home, batch)
		batch = batch[:0]
	}
	for {
		select {
		case entry, ok := <-w.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, entry)
			if len(batch) >= logBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Close 停止接收、把剩余日志落盘；幂等。超时由调用方的 ctx 控制。
func (w *logWriter) Close() {
	w.once.Do(func() {
		close(w.ch)
		w.wg.Wait()
	})
}

// appendRequestLogBatch 一次打开文件写入一批行（减少 open/close 次数）。
func appendRequestLogBatch(home string, entries []RequestLogEntry) error {
	dir := filepath.Join(home, "gateway")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "requests.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- 路径来自 GQY_HOME
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var buf []byte
	for i := range entries {
		data, err := json.Marshal(entries[i])
		if err != nil {
			continue
		}
		buf = append(buf, data...)
		buf = append(buf, '\n')
	}
	if len(buf) == 0 {
		return nil
	}
	_, err = f.Write(buf)
	return err
}

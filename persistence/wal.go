package persistence

import (
	"encoding/binary"
	"io"
	"os"
	"sync"
)

// WAL 是一个最小化的预写日志实现，用于邮箱持久化。
// 记录格式：[4 字节小端序长度][负载字节]。
//
// WAL 提供原子性的写入和读取操作，确保消息在系统崩溃后可以恢复。
// 典型使用场景：
//   - Actor 启动时重放 WAL 恢复未处理的消息
//   - 每条消息入队前先写入 WAL
//   - 消息处理完成后可以截断或压缩 WAL
type WAL struct {
	// mu 保护并发访问
	mu sync.Mutex
	// f 底层文件
	f *os.File
	// path 文件路径
	path string
}

// Open 打开或创建指定路径的 WAL。
// 文件以读写、创建、追加模式打开。
func Open(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &WAL{f: f, path: path}, nil
}

// Close 关闭底层文件。可以安全地多次调用。
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// Append 向日志追加一条记录。
// 记录格式：4 字节长度前缀 + 负载数据。
// 如果负载为空，不执行任何操作。
func (w *WAL) Append(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return os.ErrClosed
	}
	buf := make([]byte, 4+len(b))
	binary.LittleEndian.PutUint32(buf[:4], uint32(len(b)))
	copy(buf[4:], b)
	if _, err := w.f.Write(buf); err != nil {
		return err
	}
	return nil
}

// Replay 从头读取记录并按顺序返回负载。
// 截断的头部被视为日志结束。
// 重放完成后，文件指针定位到末尾，以便后续追加。
func (w *WAL) Replay() ([][]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil, os.ErrClosed
	}
	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var out [][]byte
	var lenBuf [4]byte
	for {
		_, err := io.ReadFull(w.f, lenBuf[:])
		if err != nil {
			break
		}
		n := binary.LittleEndian.Uint32(lenBuf[:])
		if n == 0 {
			continue
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(w.f, buf); err != nil {
			return nil, err
		}
		out = append(out, buf)
	}
	_, _ = w.f.Seek(0, io.SeekEnd)
	return out, nil
}

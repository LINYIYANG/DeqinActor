package actor

import (
	"bytes"
	"encoding/gob"
	"sync"
)

// Serializer 定义序列化器接口，用于消息的编码和解码。
// 序列化器用于持久化存储和远程传输时的消息转换。
type Serializer interface {
	// Marshal 将任意值序列化为字节切片。
	Marshal(v any) ([]byte, error)
	// Unmarshal 将字节切片反序列化为任意值。
	Unmarshal(b []byte) (any, error)
}

// GobSerializer 是基于 Go gob 编码的序列化器实现。
// gob 是 Go 原生的二进制序列化格式，支持所有基本类型和大多数复合类型。
// 注意：gob 编码不是跨语言兼容的，仅适用于 Go 程序之间的通信。
type GobSerializer struct {
	// mu 保护 gob 编码器/解码器的并发访问
	mu sync.Mutex
}

// Marshal 使用 gob 编码将值序列化为字节切片。
// 此方法是线程安全的，可并发调用。
func (s *GobSerializer) Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	s.mu.Lock()
	err := gob.NewEncoder(&buf).Encode(&v)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal 使用 gob 解码将字节切片反序列化为值。
// 此方法是线程安全的，可并发调用。
func (s *GobSerializer) Unmarshal(b []byte) (any, error) {
	dec := gob.NewDecoder(bytes.NewReader(b))
	var v any
	s.mu.Lock()
	err := dec.Decode(&v)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return v, nil
}

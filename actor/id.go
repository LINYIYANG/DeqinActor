package actor

import (
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
	"time"
)

var (
	// nodeID 是当前节点的唯一标识符，在程序启动时随机生成。
	// 用于确保分布式环境中生成的 Actor ID 全局唯一。
	nodeID = func() string { b := make([]byte, 6); _, _ = rand.Read(b); return hex.EncodeToString(b) }()
	// idCounter 是全局递增计数器，用于生成唯一 ID 的序列部分。
	idCounter atomic.Uint64
)

// NewActorID 生成一个新的全局唯一 Actor ID。
// ID 格式：时间戳(8字节) + 计数器(8字节) + 节点ID(6字节) = 44个十六进制字符。
// 这种设计确保了：
//   - 时间有序性：ID 包含时间戳，大致按时间排序
//   - 全局唯一性：结合时间戳、计数器和节点 ID 保证唯一
//   - 分布式友好：节点 ID 区分不同进程/机器
func NewActorID() string {
	n := idCounter.Add(1)
	ts := uint64(time.Now().UnixNano())
	b := make([]byte, 0, 8+8+6)
	tmp := make([]byte, 8)
	for i := 0; i < 8; i++ {
		tmp[i] = byte(ts >> (56 - 8*i))
	}
	b = append(b, tmp...)
	for i := 0; i < 8; i++ {
		tmp[i] = byte(n >> (56 - 8*i))
	}
	b = append(b, tmp...)
	id := hex.EncodeToString(b) + nodeID
	return id
}


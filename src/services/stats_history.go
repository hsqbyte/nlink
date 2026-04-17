package services

import (
	"sync"
	"time"
)

// StatsSnapshot 单次采样快照
type StatsSnapshot struct {
	Timestamp   int64 `json:"ts"`            // unix 秒
	TotalConns  int64 `json:"total_conns"`   // 累计连接数
	ActiveConns int64 `json:"active_conns"`  // 当前活跃连接
	BytesIn     int64 `json:"bytes_in"`      // 累计入流量
	BytesOut    int64 `json:"bytes_out"`     // 累计出流量
	PeerCount   int   `json:"peer_count"`    // 在线对端
	ProxyCount  int   `json:"proxy_count"`   // 代理数
}

// StatsHistory 统计历史环形缓冲；默认保留 1440 个采样点（采样周期 30s → 覆盖 12h）
type StatsHistory struct {
	mu       sync.RWMutex
	capacity int
	data     []StatsSnapshot
	head     int // 下一个写入位置
	filled   bool
}

// NewStatsHistory 创建指定容量的历史缓冲
func NewStatsHistory(capacity int) *StatsHistory {
	if capacity <= 0 {
		capacity = 1440
	}
	return &StatsHistory{
		capacity: capacity,
		data:     make([]StatsSnapshot, capacity),
	}
}

// Append 追加一条采样；自动覆盖最旧记录
func (h *StatsHistory) Append(s StatsSnapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.data[h.head] = s
	h.head = (h.head + 1) % h.capacity
	if h.head == 0 {
		h.filled = true
	}
}

// Snapshot 返回按时间升序排列的历史（最多 limit 条；0 表示全部）
func (h *StatsHistory) Snapshot(limit int) []StatsSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	total := h.capacity
	if !h.filled {
		total = h.head
	}
	if limit <= 0 || limit > total {
		limit = total
	}
	out := make([]StatsSnapshot, 0, limit)
	// 按时间顺序：最早的点从 head 后面开始（filled 时），否则从 0
	start := 0
	if h.filled {
		start = h.head
	}
	for i := 0; i < total; i++ {
		idx := (start + i) % h.capacity
		out = append(out, h.data[idx])
	}
	// 截取最新 limit 条
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// statsHistory 全局实例
var (
	statsHistory    *StatsHistory
	statsHistoryMu  sync.Mutex
	statsSampleStop chan struct{}
)

// StartStatsSampler 启动后台采样，sampleInterval 控制采样周期
func StartStatsSampler(sampleInterval time.Duration, capacity int) {
	statsHistoryMu.Lock()
	if statsHistory != nil {
		statsHistoryMu.Unlock()
		return
	}
	statsHistory = NewStatsHistory(capacity)
	statsSampleStop = make(chan struct{})
	stop := statsSampleStop
	statsHistoryMu.Unlock()

	go func() {
		ticker := time.NewTicker(sampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				ts := GetTunnelService()
				if ts == nil {
					continue
				}
				s := ts.ServerStats()
				statsHistory.Append(StatsSnapshot{
					Timestamp:   time.Now().Unix(),
					TotalConns:  s.TotalConns,
					ActiveConns: s.ActiveConns,
					BytesIn:     s.BytesIn,
					BytesOut:    s.BytesOut,
					PeerCount:   s.PeerCount,
					ProxyCount:  s.ProxyCount,
				})
			}
		}
	}()
}

// StopStatsSampler 停止采样（主要给测试用）
func StopStatsSampler() {
	statsHistoryMu.Lock()
	defer statsHistoryMu.Unlock()
	if statsSampleStop != nil {
		close(statsSampleStop)
		statsSampleStop = nil
	}
	statsHistory = nil
}

// GetStatsHistory 返回全局历史缓冲（可能为 nil，如果未启动采样）
func GetStatsHistory() *StatsHistory {
	statsHistoryMu.Lock()
	defer statsHistoryMu.Unlock()
	return statsHistory
}

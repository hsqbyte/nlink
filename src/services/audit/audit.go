// Package audit 提供敏感操作日志的结构化落盘 + 查询能力
//
// 文件布局: data/logs/YYYY-MM-DD/audit/audit-YYYY-MM-DD.log
// 每行一条 JSON 记录（JSONL 格式），便于 grep / jq / 滚动归档。
package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fastgox/utils/logger"
)

// Record 单条审计记录
type Record struct {
	Time      time.Time `json:"time"`
	User      string    `json:"user"`
	Role      string    `json:"role,omitempty"`
	IP        string    `json:"ip"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	RequestID string    `json:"request_id,omitempty"`
	Body      string    `json:"body,omitempty"`
}

const baseDir = "data/logs"

var (
	mu          sync.Mutex
	currentFile *os.File
	currentDate string
	retainDays  int
)

// SetRetainDays 设置审计日志保留天数 (0 = 永久)
func SetRetainDays(days int) {
	mu.Lock()
	retainDays = days
	mu.Unlock()
}

// Append 追加一条审计记录
func Append(r Record) {
	if r.Time.IsZero() {
		r.Time = time.Now()
	}
	line, err := json.Marshal(r)
	if err != nil {
		logger.Error("[Audit] 序列化失败: %v", err)
		return
	}
	line = append(line, '\n')

	mu.Lock()
	defer mu.Unlock()

	date := r.Time.Format("2006-01-02")
	if currentFile == nil || currentDate != date {
		if currentFile != nil {
			_ = currentFile.Close()
		}
		dir := filepath.Join(baseDir, date, "audit")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.Error("[Audit] 目录创建失败: %v", err)
			return
		}
		path := filepath.Join(dir, "audit-"+date+".log")
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			logger.Error("[Audit] 文件打开失败: %v", err)
			return
		}
		currentFile = f
		currentDate = date
		// 异步清理过期目录
		go cleanupExpired()
	}
	if _, err := currentFile.Write(line); err != nil {
		logger.Error("[Audit] 写入失败: %v", err)
	}
}

// QueryFilter 查询条件
type QueryFilter struct {
	Date   string // YYYY-MM-DD; 留空 = 今天
	User   string // 完全匹配
	Path   string // 子串匹配
	Method string // 完全匹配 (大写)
	Limit  int    // 最多返回条数 (默认 100, 上限 1000)
	Offset int    // 偏移
}

// Query 按条件读取审计记录 (按时间倒序返回)
func Query(f QueryFilter) ([]Record, int, error) {
	if f.Date == "" {
		f.Date = time.Now().Format("2006-01-02")
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
	path := filepath.Join(baseDir, f.Date, "audit", "audit-"+f.Date+".log")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer file.Close()

	// 简单实现：全部读入 + 反序排序。审计日志日量级一般 < 10MB，可承受。
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, err
	}
	lines := splitLines(data)
	all := make([]Record, 0, len(lines))
	for _, ln := range lines {
		if len(ln) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(ln, &r); err != nil {
			continue
		}
		if f.User != "" && r.User != f.User {
			continue
		}
		if f.Method != "" && !strings.EqualFold(r.Method, f.Method) {
			continue
		}
		if f.Path != "" && !strings.Contains(r.Path, f.Path) {
			continue
		}
		all = append(all, r)
	}
	total := len(all)
	sort.Slice(all, func(i, j int) bool { return all[i].Time.After(all[j].Time) })
	start := f.Offset
	if start > total {
		start = total
	}
	end := start + f.Limit
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

func splitLines(b []byte) [][]byte {
	out := make([][]byte, 0, 32)
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// cleanupExpired 删除超出 retainDays 的审计目录
func cleanupExpired() {
	mu.Lock()
	d := retainDays
	mu.Unlock()
	if d <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -d).Format("2006-01-02")
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) != 10 || name >= cutoff {
			continue
		}
		auditDir := filepath.Join(baseDir, name, "audit")
		if _, err := os.Stat(auditDir); err == nil {
			if err := os.RemoveAll(auditDir); err == nil {
				logger.Info("[Audit] 已清理过期审计日志: %s", auditDir)
			}
		}
	}
}

// Close 关闭文件 (供测试 / 优雅关闭使用)
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if currentFile != nil {
		_ = currentFile.Close()
		currentFile = nil
	}
}

// nopCloser 内部用：占位（避免未使用 import 警告，预留扩展点）
var _ = fmt.Sprintf
